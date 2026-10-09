package sdkv1

import "log"

// ChainSignals composes signal handlers into one, so the plugin's single signal
// port (Plugin.OnSignal) serves any number of them. Each handler sees every
// signal, in order. A panic in one is recovered and logged, so it cannot keep
// the handlers after it — a cancellation, say — from seeing the signal. Nil
// handlers are skipped.
//
//	p.OnSignal(sdkv1.ChainSignals(stops.OnSignal, audit))
func ChainSignals(handlers ...SignalHandler) SignalHandler {
	return func(sig Signal) {
		for _, handler := range handlers {
			if handler == nil {
				continue
			}
			func() {
				defer func() {
					if r := recover(); r != nil {
						log.Printf("signal handler panicked on %s: %v", sig.Subject, r)
					}
				}()
				handler(sig)
			}()
		}
	}
}
