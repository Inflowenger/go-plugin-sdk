package sdkv1

import (
	"context"

	"github.com/nats-io/nats.go"
)

type ReqStatus int8

const (
	ACCEPT ReqStatus = 1
	REJECT ReqStatus = -1
)

type Job struct {
	Action   string
	Progress Progress
	JobId    string
	ctx      context.Context
}
type Response struct {
	Data  map[string]any `json:"data"`
	Error any            `json:"error"`
}

type ActionRequest struct {
	JobId  string
	Action string
	status ReqStatus
	Body   RequestBody
	msg    *nats.Msg
}

func (r *ActionRequest) Accept() Job {
	r.msg.Respond([]byte(`{}`))
	return Job{Action: r.Action, JobId: r.JobId}
}
func (r *ActionRequest) AcceptWithCtx(ctx context.Context) Job {
	r.msg.Respond([]byte(`{}`))
	return Job{Action: r.Action, JobId: r.JobId, ctx: ctx}
}
func (r *ActionRequest) Reject(cause string) {
	r.msg.Respond([]byte(cause))
}

type Progress struct {
	Progress int            `json:"progress"`
	Details  map[string]any `json:"details"`
}
