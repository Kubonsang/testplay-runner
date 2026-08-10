//go:build windows

package vhdxworkspace

import (
	"context"
	"errors"
	"time"

	"golang.org/x/sys/windows/svc"
)

const WindowsServiceName = "TestPlayStorageBroker"

func RunWindowsService(configPath string) error {
	config, err := LoadServiceConfig(configPath)
	if err != nil {
		return err
	}
	broker, err := NewBroker(config.BrokerConfig(), NewNative())
	if err != nil {
		return err
	}
	if _, err := broker.Recover(context.Background(), 30*time.Second); err != nil {
		return err
	}
	return svc.Run(WindowsServiceName, &brokerService{broker: broker, config: config})
}

func RunBrokerConsole(ctx context.Context, configPath string) error {
	config, err := LoadServiceConfig(configPath)
	if err != nil {
		return err
	}
	broker, err := NewBroker(config.BrokerConfig(), NewNative())
	if err != nil {
		return err
	}
	if _, err := broker.Recover(ctx, 30*time.Second); err != nil {
		return err
	}
	server := &PipeServer{Name: config.PipeName, AllowedSID: config.UserSID, Broker: broker}
	return server.Serve(ctx)
}

type brokerService struct {
	broker *Broker
	config ServiceConfig
}

func (service *brokerService) Execute(_ []string, requests <-chan svc.ChangeRequest, changes chan<- svc.Status) (bool, uint32) {
	const accepts = svc.AcceptStop | svc.AcceptShutdown
	changes <- svc.Status{State: svc.StartPending}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		server := &PipeServer{Name: service.config.PipeName, AllowedSID: service.config.UserSID, Broker: service.broker}
		done <- server.Serve(ctx)
	}()
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_, _ = service.broker.Recover(ctx, 30*time.Second)
			}
		}
	}()
	changes <- svc.Status{State: svc.Running, Accepts: accepts}
	for {
		select {
		case request := <-requests:
			switch request.Cmd {
			case svc.Interrogate:
				changes <- request.CurrentStatus
			case svc.Stop, svc.Shutdown:
				changes <- svc.Status{State: svc.StopPending}
				cancel()
				// ConnectNamedPipe is synchronous. A local authenticated connection
				// wakes the listener so it can observe cancellation.
				go func() {
					request := NewRequest(OperationHello, "service-stop")
					_, _ = (PipeClient{Name: service.config.PipeName}).Call(context.Background(), request)
				}()
				select {
				case err := <-done:
					if err != nil && !errors.Is(err, context.Canceled) {
						return true, 1
					}
				case <-time.After(10 * time.Second):
					return true, 2
				}
				return false, 0
			}
		case err := <-done:
			if err != nil && !errors.Is(err, context.Canceled) {
				return true, 3
			}
			return false, 0
		}
	}
}
