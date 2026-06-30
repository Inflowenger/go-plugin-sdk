package sdkv1

import (
	"fmt"

	"github.com/nats-io/nats.go"
)

type ReqStatus int8

const (
	ACCEPT ReqStatus = 1
	REJECT ReqStatus = -1
)

type Response struct {
	Data  map[string]any `json:"data"`
	Error any            `json:"error"`
}

type ActionRequest struct {
	JobId  string
	Action string
	status ReqStatus
	Req    Request
	msg    *nats.Msg
}

func (r *ActionRequest) Accept() Job {
	r.msg.Respond([]byte(fmt.Sprintf(`{"jobId":"%s"}`, r.JobId)))
	return Job{plugin:r.Req.Plugin,Action: r.Action, JobId: r.JobId}
}

func (r *ActionRequest) Reject(cause string) {
	r.msg.Respond([]byte(cause))
}

type Progress struct {
	Progress int            `json:"progress"`
	Details  map[string]any `json:"details"`
}
