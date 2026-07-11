package can

import (
	"fmt"
	"sync"
	"time"

	"virtual_bess/internal/simulator"
	"virtual_bess/internal/zaplog"
)

// canBus 抽象一路 CAN 接口的收发，屏蔽平台差异（Linux socketcan / 其他平台 stub）。
type canBus interface {
	// Send 发送一帧扩展 CAN 报文。
	Send(f canFrame) error
	// Recv 阻塞读取一帧，返回不含 EFF 标志的 CAN ID 与数据；接口关闭时返回错误。
	Recv() (id uint32, data []byte, err error)
	// Close 关闭底层 socket，使阻塞中的 Recv 返回。
	Close() error
}

// dialCAN 由平台相关文件实现：Linux 打开 socketcan raw socket，其他平台返回不支持错误。
var dialCAN func(iface string) (canBus, error)

// CANPublisher 把一套 BMS 的寄存器周期性广播为博最 CAN 数据帧，
// 并监听 EMS→BCM 命令帧，把控制写回对应 BMS slave。
type CANPublisher struct {
	iface   string
	slaveID uint8
	bank    *simulator.SlaveBank
	period  time.Duration
	apply   func(reg, val uint16) // 把命令写回 BMS（经 sim 锁 + 写回调）
	bus     canBus

	wg     sync.WaitGroup
	closed chan struct{}
}

// StartPublishers 按配置为每个 CAN 设备打开接口并启动收发协程。
// 任一接口打开失败即返回错误（fail fast），由调用方决定是否终止进程。
func StartPublishers(cfg simulator.CANConfig, sim *simulator.Simulator) ([]*CANPublisher, error) {
	if !cfg.Enabled {
		return nil, nil
	}
	var pubs []*CANPublisher
	for _, d := range cfg.Devices {
		bank := sim.BankForSlave(d.BMSSlaveID)
		if bank == nil {
			ClosePublishers(pubs)
			return nil, fmt.Errorf("unknown slave id %d", d.BMSSlaveID)
		}
		bus, err := dialCAN(d.Interface)
		if err != nil {
			ClosePublishers(pubs)
			return nil, err
		}
		slaveID := d.BMSSlaveID
		p := &CANPublisher{
			iface:   d.Interface,
			slaveID: slaveID,
			bank:    bank,
			period:  time.Duration(d.BroadcastPeriodMs) * time.Millisecond,
			apply: func(reg, val uint16) {
				if err := sim.WriteHoldingExternal(slaveID, reg, val); err != nil {
					zaplog.Errorf("CAN command apply failed on bms %d reg %d: %v", slaveID, reg, err)
				}
			},
			bus:    bus,
			closed: make(chan struct{}),
		}
		p.start()
		pubs = append(pubs, p)
		zaplog.Infof("CAN publisher started: iface=%s bms=%d period=%dms", d.Interface, slaveID, d.BroadcastPeriodMs)
	}
	return pubs, nil
}

func ClosePublishers(pubs []*CANPublisher) {
	for _, p := range pubs {
		p.Close()
	}
}

func (p *CANPublisher) start() {
	p.wg.Add(2)
	go p.broadcastLoop()
	go p.recvLoop()
}

// broadcastLoop 每个周期读取 BMS 寄存器快照并逐帧发送。
func (p *CANPublisher) broadcastLoop() {
	defer p.wg.Done()
	ticker := time.NewTicker(p.period)
	defer ticker.Stop()
	for {
		select {
		case <-p.closed:
			return
		case <-ticker.C:
			for _, f := range buildBMSFrames(p.bank) {
				if err := p.bus.Send(f); err != nil {
					select {
					case <-p.closed:
						return
					default:
					}
					zaplog.Errorf("CAN send failed on %s id=0x%X: %v", p.iface, f.id, err)
					break
				}
			}
		}
	}
}

// recvLoop 阻塞读取命令帧并写回 BMS 控制寄存器。
func (p *CANPublisher) recvLoop() {
	defer p.wg.Done()
	for {
		id, data, err := p.bus.Recv()
		if err != nil {
			select {
			case <-p.closed:
				return
			default:
			}
			zaplog.Errorf("CAN recv failed on %s: %v", p.iface, err)
			return
		}
		if reg, val, ok := decodeBMSCommand(id, data); ok {
			zaplog.Infof("CAN command on %s: id=0x%X -> bms %d reg %d = %d", p.iface, id, p.slaveID, reg, val)
			p.apply(reg, val)
		}
	}
}

// Close 停止收发协程并关闭底层 socket。
func (p *CANPublisher) Close() {
	select {
	case <-p.closed:
		// 已关闭
	default:
		close(p.closed)
	}
	if p.bus != nil {
		_ = p.bus.Close()
	}
	p.wg.Wait()
}
