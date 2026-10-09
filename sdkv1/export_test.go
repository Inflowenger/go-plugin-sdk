package sdkv1

import "github.com/nats-io/nats.go"

// RunPipeline runs one request of action through p's pipeline — to the end, once
// the handler has returned — with the job's commands going to runtime. It
// exposes the request path to tests outside the package: the ones exercising
// middleware built on the public API, such as jobstop, which sdkv1's own tests
// cannot import without a cycle. Compiled into tests only.
//
// body, when given, is the request payload the pipeline sees as job.Req.Data —
// for middleware that reads the `{_registry, body}` envelope at the accept
// stage.
func RunPipeline(p *Plugin, action Action, runtime IPlugin, body ...[]byte) {
	if action.Method == "" {
		action.Method = "run"
	}
	var data []byte
	if len(body) > 0 {
		data = body[0]
	}
	p.runPipeline(action, Request{Plugin: runtime, Data: data}, &nats.Msg{})
}
