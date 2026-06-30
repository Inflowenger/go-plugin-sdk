package inflowpluginsdk

import (
	"fmt"
	"testing"

	"github.com/Inflowenger/go-plugin-sdk/sdkv1"
)



func TestInit(t *testing.T) {
	p,err:=sdkv1.NewPlugin(sdkv1.WithDotEnv(".env.inflow"))
	if err!=nil{
		panic(err)
	}
	p.Intro(sdkv1.PluginIntro{Name: "Mapper",Author: "inflow Dev. Team",Version: "v0.0.1"})
	fmt.Println(p)
	p.AddAction(sdkv1.Action{Method: "get.acc.list",RequestHandler: func(ar sdkv1.ActionRequest) {
		job:=ar.Accept()
		job.Done(map[string]any{"p1_res":12000.111})
	
	}})
	p.Start()
	select{}
}