package platform

type mdnsPublisher interface {
	SetText([]string) error
	Shutdown()
}
