package sdkv1

import (
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
}

func (j *Job) Done(data map[string]any, key ...string) any {

	return j.Command(ProgressCommand, CommandPayload{Progress: 100, Details: data, CommitOn: strings.Join(key, ".")})

}
func (j *Job) DoneWithError(error string) any {

	return j.Command(ProgressCommand, CommandPayload{Progress: 100, Details: map[string]any{"error": error}})

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
func (j *Job) CmdSvcCall(data any, opData map[string]any) any {
	envelop := ExtSvcRequestBody{Data: data, OperationData: opData}
	reqBody, _ := sonic.Marshal(envelop)
	sub := j.makeJobSubject(JobCommandRequest)
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
func (j *Job) CmdStopFlow() any {
	sub := j.makeJobSubject(StopCommand)
	msg, err := j.send(sub, nil)
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
