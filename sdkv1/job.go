package sdkv1

import (
	"fmt"
	"log"

	"github.com/bytedance/sonic"
)

type Job struct {
	plugin IPlugin
	Action string
	JobId  string
}

func (j *Job) Done(data map[string]any) any {

	return j.Command(ProgressCommand, CommandPayload{Progress: 100, Details: data})

}
func (j *Job) DoneWithError(error string) any {

	return j.Command(ProgressCommand, CommandPayload{Progress: 100, Details: map[string]any{"error": error}})

}
func (j *Job) Progress(data CommandPayload) any {

	return j.Command(ProgressCommand, data)

}

func (j *Job) Command(cmd Command, data CommandPayload) any {

	sub := j.makeJobSubject(cmd)
	dataByte, err := sonic.Marshal(data)
	if err != nil {
		log.Println("progress command ", cmd, " error:", err)
		return err
	}
	msg, err := j.plugin.Send(sub, dataByte)
	if err != nil {
		return err
	}
	return msg.Data
}

// makeJobSubject creates a subject for job updates (CPU pattern)
func (j *Job) makeJobSubject(cmd Command) string {
	return fmt.Sprintf("inflow.cpu.%s.%s.%s", j.plugin.GetPluginId(), j.JobId, cmd)
}

// "context/path"
// makeFormSubject creates a subject for getting data from current context at runtime
func (j *Job) makeGetDataSubject() string {
	return fmt.Sprintf("inflow.cpu.%s.%s.%s", j.plugin.GetPluginId(), j.JobId, "context/path")
}

// "context/current"
func (j *Job) makeGetCurrentPathSubject() string {
	return fmt.Sprintf("inflow.cpu.%s.%s.%s", j.plugin.GetPluginId(), j.JobId, "context/current")
}

// "commit"
func (j *Job) makeCommitOnPathSubject() string {
	return fmt.Sprintf("inflow.cpu.%s.%s.%s", j.plugin.GetPluginId(), j.JobId, "commit")
}
