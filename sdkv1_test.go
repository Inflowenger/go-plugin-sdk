package inflowpluginsdk

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/Inflowenger/go-plugin-sdk/sdkv1"
)

func TestInit(t *testing.T) {
	p, err := sdkv1.NewPlugin(sdkv1.WithDotEnv(".env.inflow"))
	if err != nil {
		panic(err)
	}
	p.Intro(sdkv1.PluginIntro{Name: "HTTP.CALL", Author: "inflow Dev. Team", Version: "v0.0.1"})
	p.AddAction(sdkv1.Action{Method: "http.call", RequestHandler: func(ar sdkv1.ActionRequest) {
		fmt.Println(string(ar.Req.Msg.Data))
		recvMsg, err := sdkv1.CastRequestTo[struct {
			Url    string `json:"url"`
			Method string `json:"method"`
		}](ar.Req.Msg.Data)
		if err != nil {
			ar.Reject(err.Error())
			return
		}
		if prevJobId, ok := recvMsg.Registry["jobId"]; ok {
			fmt.Printf("This Node in Previous Run has JobId: %s and done At %v\n", prevJobId, time.Unix(int64(recvMsg.Registry["doneAt"].(float64)), 0))
		}
		job := ar.Accept()
		fmt.Printf("REQUEST URL: %s\n", recvMsg.Body.Url)

		job.Progress(10, sdkv1.Frame{Title: "init step", Content: "given task is in progress"})
		job.Progress(20, sdkv1.Frame{Title: "working"}) //mock process consume

		// make new http req
		req, err := http.NewRequest(recvMsg.Body.Method, recvMsg.Body.Url, nil)
		if err != nil {
			job.DoneWithError(err.Error())
			return
		}
		req.Header.Set("Accept", "application/text")
		req.Header.Add("Accept","application/json")
		req.Header.Set("User-Agent","Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/149.0.0.0 Safari/537.36")
		client := &http.Client{}
		// Send request
		resp, err := client.Do(req)
		if err != nil {
			fmt.Println("Error sending request:", err)
			job.DoneWithError(err.Error())

			return
		}
		defer resp.Body.Close()
		resBody, err := io.ReadAll(resp.Body)
		if err != nil {
			job.DoneWithError(err.Error())
			return
		}
		fmt.Println(string(resBody))
		doneBody := map[string]any{}
		err = json.Unmarshal(resBody, &doneBody)
		if err != nil {
			doneBody["rawBody"] = string(resBody)

		}
		job.Progress(50, sdkv1.Frame{Title: "almost done"}) // mock process consume
		////////////////
		job.Progress(80, sdkv1.Frame{Title: "almost done"}) // mock process consume

		job.Done(doneBody)

	}})

		p.AddAction(sdkv1.Action{Method: "fn", RequestHandler: func(ar sdkv1.ActionRequest) {
		job := ar.Accept()
		cur:=job.CmdGetCurrentScope()
		if d,ok:=cur.([]byte);ok{
			fmt.Println("GetCurrent",string(d))
		}
		scope:=job.CmdGetScope("$.OPA")
		if d,ok:=scope.([]byte);ok{
			fmt.Println("Scope : ",string(d))
		}
		// job.CmdStopFlow()
		job.Done(map[string]any{"action":"done"})
	}})
	p.Start()
	select {}
}


func TestCommands(t *testing.T) {
	p, err := sdkv1.NewPlugin(sdkv1.WithDotEnv(".env.inflow"))
	if err != nil {
		panic(err)
	}
	p.Intro(sdkv1.PluginIntro{Name: "RPC", Author: "inflow Dev. Team", Version: "v0.0.1"})
	p.AddAction(sdkv1.Action{Method: "fn", RequestHandler: func(ar sdkv1.ActionRequest) {
		job := ar.Accept()
		cur:=job.CmdGetCurrentScope()
		if d,ok:=cur.([]byte);ok{
			fmt.Println("GetCurrent",string(d))
		}
		scope:=job.CmdGetScope("$.OPA")
		if d,ok:=scope.([]byte);ok{
			fmt.Println("Scope : ",string(d))
		}
		// job.CmdStopFlow()
		job.Done(map[string]any{"action":"done"})
	}})
	p.Start()
	select{}
}