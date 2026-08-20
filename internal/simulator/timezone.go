package simulator

import (
	"fmt"
	"time"
	// 内嵌 IANA 时区库：现场/容器里常常没装 tzdata，
	// 缺库时 time.LoadLocation 会直接失败，导致按站点时区仿真不可用。
	_ "time/tzdata"
)

// siteLocation 保存站点时区，PV 日照曲线、日/月/年电量翻转和负荷曲线
// 都按它换算成站点本地时钟；默认跟随运行主机的本地时区。
var siteLocation = time.Local

// SetSiteTimezone 设置站点时区。
//
// 输入 tz 为 IANA 时区名（如 "Europe/Riga"），空字符串表示跟随主机本地时区。
// 时区非法时返回错误而不静默降级——降级会让仿真曲线与预期整体错位数小时，
// 且很难在现场被发现。
// 副作用：修改包级 siteLocation，应在仿真启动前调用一次。
func SetSiteTimezone(tz string) error {
	loc, err := resolveLocation(tz)
	if err != nil {
		return err
	}
	siteLocation = loc
	return nil
}

// SiteLocation 返回当前生效的站点时区，供展示和日志使用。
func SiteLocation() *time.Location { return siteLocation }

// resolveLocation 把配置里的时区名解析为 *time.Location，空值回落到主机本地时区。
func resolveLocation(tz string) (*time.Location, error) {
	if tz == "" {
		return time.Local, nil
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return nil, fmt.Errorf("invalid timezone %q: %w", tz, err)
	}
	return loc, nil
}
