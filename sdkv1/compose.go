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

// LogSignals returns a SignalHandler that prints one line per signal that lands
// on the plugin's signal port, so what the runtime published is visible in the
// plugin's log the moment it arrives. `prefix` names the plugin in the line
// (pass "" for none):
//
//	ai-decision: signal proc job=<uuid> conclusion=flow_stop_by_user canceled=true succeeded=false
//
// It is read-only — acting on a conclusion is another handler's job — so it
// belongs beside the one that does, which is why Plugin.OnSignal takes a single
// handler and this composes:
//
//	p.OnSignal(sdkv1.ChainSignals(sdkv1.LogSignals("ai-decision"), stops.OnSignal))
//
// Registering a cancellation handler alone replaces the default handler
// OnSignal(nil) installs, and with it the only sight of the port; chain this to
// keep it.
//
// This is the signal ARRIVING, which is not the same event as a job being cut
// short: jobstop.Registry.OnSignal logs that separately, for the jobs it holds.
// The two lines together read as the whole story — what the runtime said, and
// what this process did about it — and a signal with no cancel line was for a
// job this process is not running, or concluded a job that was already done.
//
// Note what a logged jobId does NOT mean. The runtime publishes process signals
// on ONE subject per plugin, so every process of a plugin sees every one of that
// plugin's signals: jobs of other flows running at the same time, and, when the
// plugin runs as several replicas, jobs this process never accepted. Lines for
// jobs this process knows nothing about are the ordinary case.
//
// A kind this SDK does not model carries no typed fields, so its line gives the
// subject and the raw payload instead.
func LogSignals(prefix string) SignalHandler {
	if prefix != "" {
		prefix += ": "
	}
	return func(sig Signal) {
		if sig.Kind != RuntimeProcessSignal {
			log.Printf("%ssignal %s subject=%s data=%s", prefix, sig.Kind, sig.Subject, sig.Data)
			return
		}
		log.Printf("%ssignal %s job=%s conclusion=%s canceled=%v succeeded=%v",
			prefix, sig.Kind, sig.JobId, sig.Conclusion, sig.Conclusion.Canceled(), sig.Conclusion.Succeeded())
	}
}
