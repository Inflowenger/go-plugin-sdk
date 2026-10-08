package sdkv1

import (
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
	// release ends this job's context and unfiles it from its plugin's
	// registry, set when NewCancelableJobHandler files the job and run by
	// Command on the terminal command. A func value, so every copy of the job
	// handed on from then — a helper taking the job by value and calling Done —
	// still releases it. Nil for a job declared with a plain JobHandler.
	release func()
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

	// A terminal command ends the process — the runtime concludes a job on
	// progress above 99 and on nothing else (next_tags only records routing) —
	// so a job filed by NewCancelableJobHandler leaves the registry here. Deferred, so
	// the command is on the wire before the job's context is released, and
	// unconditional on the outcome: the handler has declared the job over and
	// will not report on it again.
	if cmd == ProgressCommand && data.Progress > 99 && j.release != nil {
		defer j.release()
	}

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