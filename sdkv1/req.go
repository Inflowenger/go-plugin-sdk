package sdkv1

import (
	"fmt"

	"github.com/bytedance/sonic"
	"github.com/nats-io/nats.go"
)

type ReqStatus int8

const (
	ACCEPT ReqStatus = 1
	REJECT ReqStatus = -1
)
type JobHandler func(req Job )
type Response struct {
	Data  map[string]any `json:"data"`
	Error any            `json:"error"`
}

type ActionRequest struct {
	JobId  string
	Action string
	Req    Request
}

func (r *ActionRequest) Accept(msg *nats.Msg) Job {
	j:=Job{plugin:r.Req.Plugin,Action: r.Action, JobId: r.JobId,Req: r.Req}
	msg.Respond([]byte(fmt.Sprintf(`{"jobId":"%s"}`, r.JobId)))
	return j
}

func (r *ActionRequest) Reject(msg *nats.Msg,cause string) {
	msg.Respond([]byte(cause))
}

func CastRequestTo[T any](msg []byte)(*RequestBody[T],error){
	body:=RequestBody[T]{}
	err:=sonic.Unmarshal(msg,&body)
	if err!=nil{
		return nil,err
	}
	return &body,nil
}

func WithJobHandler(jobHanlder JobHandler)func(ar ActionRequest,msg *nats.Msg){
	return func(ar ActionRequest,msg *nats.Msg) {
		job:=ar.Accept(msg)
		jobHanlder(job)
	}
}