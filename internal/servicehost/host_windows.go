//go:build windows

package servicehost

import (
	"sync"

	"golang.org/x/sys/windows/svc"
)

func IsService() bool {
	isService, err := svc.IsWindowsService()
	return err == nil && isService
}

func Run(name string, run func(<-chan struct{}, <-chan struct{}) error) error {
	return svc.Run(name, &handler{run: run})
}

type handler struct {
	run func(<-chan struct{}, <-chan struct{}) error
}

func (h *handler) Execute(_ []string, requests <-chan svc.ChangeRequest, statuses chan<- svc.Status) (bool, uint32) {
	statuses <- svc.Status{State: svc.StartPending}

	stop := make(chan struct{})
	reload := make(chan struct{}, 1)
	done := make(chan error, 1)
	go func() {
		done <- h.run(stop, reload)
	}()

	accepted := svc.AcceptStop | svc.AcceptShutdown | svc.AcceptParamChange
	statuses <- svc.Status{State: svc.Running, Accepts: accepted}
	var stopOnce sync.Once
	for {
		select {
		case err := <-done:
			statuses <- svc.Status{State: svc.StopPending}
			if err != nil {
				return false, 1
			}
			return false, 0
		case request := <-requests:
			switch request.Cmd {
			case svc.Interrogate:
				statuses <- request.CurrentStatus
			case svc.ParamChange:
				select {
				case reload <- struct{}{}:
				default:
				}
			case svc.Stop, svc.Shutdown:
				statuses <- svc.Status{State: svc.StopPending}
				stopOnce.Do(func() { close(stop) })
			default:
				statuses <- svc.Status{State: svc.Running, Accepts: accepted}
			}
		}
	}
}
