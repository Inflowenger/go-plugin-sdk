package sdkv1

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"

	"github.com/bytedance/sonic"
	"github.com/nats-io/nats.go"
)

type Job struct {
	plugin IPlugin
	Action string
	JobId  string
	Req    Request
	ctx    context.Context
}

// Context is the job's context: the one its middleware passed down (see
// Middleware), carrying the jobId (JobIDFromContext) and whatever the middleware
// bound to it, and ended by the SDK when the handler returns. A job built by
// hand answers context.Background().
//
// Like an http.Request's, it lives as long as the handler: work the handler
// leaves running after it returns must not hold it — derive that work's context
// with context.WithoutCancel, which keeps the values (a trace) and drops the
// cancellation.
func (j Job) Context() context.Context {
	if j.ctx == nil {
		return context.Background()
	}
	return j.ctx
}

// WithContext returns a copy of the job carrying ctx as its Context(). The SDK
// uses it to hand a handler what its middleware passed down; a handler can use
// it to pass a narrowed context along with the job.
func (j Job) WithContext(ctx context.Context) Job {
	j.ctx = ctx
	return j
}

func (j *Job) Done(data map[string]any, key ...string) any {

	return j.Command(ProgressCommand, CommandPayload{Progress: 100, Details: data, CommitOn: strings.Join(key, ".")})

}

// DoneWithError ends the job as failed, reporting `error` as the reason.
//
// The reason no longer travels as a detail: it goes in CommandPayload.Error, its
// own field on the terminal command, and Details is left untouched. So the job
// commits nothing and the flow sees a failure with a message.
func (j *Job) DoneWithError(error string) any {

	return j.DoneWithErrorCode(0, error, nil)

}

// DoneWithErrorData ends the job as failed exactly like DoneWithError, but keeps
// a payload: `data` is reported (and committed, at `key` when given) alongside
// the reason. Nothing in `data` is reserved — the reason rides on its own field,
// so a key named "error" is now the plugin's to use.
//
// Use it when the failure still carries something the flow needs: the state the
// node reached, a partial result, or scope the node must not drop. That last one
// matters because a terminal command's details ARE what gets committed onto the
// node's scope — a bare DoneWithError commits nothing, so anything the node had
// persisted there (a conversation, a cursor) is gone by the next read. Hand it
// back through `data` to keep it.
func (j *Job) DoneWithErrorData(error string, data map[string]any, key ...string) any {

	return j.DoneWithErrorCode(0, error, data, key...)

}

// DoneWithErrorCode is DoneWithErrorData with the plugin's own error number
// attached. `code` belongs to the plugin's numbering — the core carries it next
// to the message and never interprets it — so pass 0 when the plugin has none.
func (j *Job) DoneWithErrorCode(code int, error string, data map[string]any, key ...string) any {

	return j.Command(ProgressCommand, CommandPayload{
		Progress: 100,
		Details:  data,
		CommitOn: strings.Join(key, "."),
		Error:    &ErrorPayload{Code: code, Message: error},
	})

}

// progress is percentage of doing job and 100 or greater that 100 makes job to done job
func (j *Job) Progress(porgresPercent int, Step Frame) any {
	return j.Command(ProgressCommand, CommandPayload{Progress: porgresPercent, Frame: Step})
}
func (j *Job) CmdGetCurrentScope() any {
	sub := j.makeJobSubject(ContextCurrentCommand)
	msg, err := j.send(sub, nil)
	if err != nil {
		return err
	}
	return msg.Data
}
func (j *Job) CmdNextFilter(nextsTags []string) any {
	sub := j.makeJobSubject(JobCommandNextTags)
	msg, err := j.send(sub, []byte(strings.Join(nextsTags, ",")))
	if err != nil {
		return err
	}
	return msg.Data
}
func (j *Job) CmdSvcCall(action string ,data any, opData map[string]any) any {
	if strings.TrimSpace(action)==""{
		return errors.New("invalid subject")
	}
	envelop := CallSvcBody{ Data: data, OperationData: opData}
	reqBody, _ := sonic.Marshal(envelop)
	sub := j.makeCallSvcSubject(Command(action))
	msg, err := j.send(sub, []byte(reqBody))
	if err != nil {
		return err
	}
	return msg.Data
}
func (j *Job) CmdGetScope(jsonPath string) any {
	sub := j.makeJobSubject(ContextPathCommand)
	msg, err := j.send(sub, []byte(jsonPath))
	if err != nil {
		return err
	}
	return msg.Data
}
func (j *Job) CmdSetOnPath(jsonPath string, data map[string]any) any {
	dataContent := JobBodyContent{
		CommitOn: jsonPath,
		Details:  data,
	}
	sub := j.makeJobSubject(JobCommandCommit)
	bData, err := sonic.Marshal(dataContent)
	if err != nil {
		return err
	}
	msg, err := j.send(sub, bData)
	if err != nil {
		return err
	}
	return msg.Data
}
func (j *Job) Command(cmd Command, data CommandPayload) any {

	sub := j.makeJobSubject(cmd)
	dataByte, err := sonic.Marshal(data)
	if err != nil {
		log.Println("progress command ", cmd, " error:", err)
		return err
	}
	msg, err := j.send(sub, dataByte)
	if err != nil {
		return err
	}
	return msg.Data
}

func (j *Job) send(sub string, data []byte) (*nats.Msg, error) {
	return j.plugin.Send(sub, data)
}

// makeJobSubject creates a subject for job updates (CPU pattern)
func (j *Job) makeJobSubject(cmd Command) string {
	return fmt.Sprintf("inflow.cpu.%s.%s.%s", j.plugin.GetPluginId(), j.JobId, cmd)
}

// makeJobSubject creates a subject for job updates (CPU pattern)
func (j *Job) makeCallSvcSubject(action Command) string {
	return fmt.Sprintf("inflow.cpu.%s.%s.%s.%s", j.plugin.GetPluginId(), j.JobId, JobCommandRequest,action)
}