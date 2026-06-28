package simulator

type IEC61850Service interface {
	Sync()
	Close()
}

type noopIEC61850Service struct{}

func (noopIEC61850Service) Sync()  {}
func (noopIEC61850Service) Close() {}
