package sdkv1_test

import (
	"context"
	"testing"

	"github.com/Inflowenger/go-plugin-sdk/sdkv1"
)

// The observer form of a plugin: work that outlives the flow's process, picked
// up again on a later run through `_registry`. See docs/detached-work.md.
//
// The properties below are the ones the pattern rests on — the request (so
// `_registry`) is readable at the accept stage, the adopted handle becomes the
// job, and "not yet" is a routed success.

type input struct {
	Project string `json:"project"`
	Query   string `json:"query"`
}

func request(registryJobId string) []byte {
	if registryJobId == "" {
		return []byte(`{"_registry":{},"body":{"project":"acme/api","query":"cpg"}}`)
	}
	return []byte(`{"_registry":{"jobId":"` + registryJobId + `","reqAt":1782773000,"doneAt":0,` +
		`"conclusion":"done"},"body":{"project":"acme/api","query":"cpg"}}`)
}

// attachOrStart is the pattern's accept-stage decision, with the service's
// memory of its own work stubbed by `held`.
func attachOrStart(held map[string]bool, fresh string, started *[]string) sdkv1.MiddlewareFunc {
	return func(ctx context.Context, job sdkv1.Job) (context.Context, error) {
		in, err := sdkv1.CastRequestTo[input](job.Req.Data)
		if err != nil {
			return nil, err
		}
		if prev, ok := in.Registry["jobId"].(string); ok && prev != "" && held[prev] {
			return sdkv1.WithJobIDContext(ctx, prev), nil // observe; start nothing
		}
		*started = append(*started, in.Body.Query) // the upstream registration
		return sdkv1.WithJobIDContext(ctx, fresh), nil
	}
}

// A later run adopts the handle its own previous run left in `_registry`, starts
// no new work, and addresses the external job by the one shared id.
func TestRegistryHandleIsAdoptedAtAccept(t *testing.T) {
	p, _ := sdkv1.NewPlugin(sdkv1.WithPluginId("plug-1"))
	prev := "q-cpg-8f21c47b"
	var started []string
	rt := &runtime{}

	var observed string
	sdkv1.RunPipeline(p, sdkv1.Action{
		Middleware: sdkv1.Use(attachOrStart(map[string]bool{prev: true}, "q-new-00000000", &started)),
		RequestHandler: func(job sdkv1.Job) {
			observed = job.JobId
			// "not yet": route the pending port and end the job as a success.
			job.CmdNextFilter([]string{"pending"})
			job.Done(map[string]any{"state": "running", "percent": 38}, "joern")
		},
	}, rt, request(prev))

	if observed != prev {
		t.Errorf("the run observed %q, want the handle from _registry (%q)", observed, prev)
	}
	if len(started) != 0 {
		t.Errorf("new upstream work was started (%v) although the previous job was still held", started)
	}
	want := []string{
		"inflow.cpu.plug-1." + prev + ".next_tags",
		"inflow.cpu.plug-1." + prev + ".progress",
	}
	if got := rt.sent; len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("commands went to %v, want %v: routed, then finished, under the shared id", got, want)
	}
}

// With no memory — a first run, or a run over a new context document — the
// plugin starts work and adopts the id the service gives back.
func TestNoRegistryHandleStartsNewWork(t *testing.T) {
	p, _ := sdkv1.NewPlugin(sdkv1.WithPluginId("plug-1"))
	var started []string
	var got string
	sdkv1.RunPipeline(p, sdkv1.Action{
		Middleware:     sdkv1.Use(attachOrStart(nil, "q-new-00000000", &started)),
		RequestHandler: func(job sdkv1.Job) { got = job.JobId },
	}, &runtime{}, request(""))

	if len(started) != 1 {
		t.Errorf("upstream registrations = %v, want exactly one", started)
	}
	if got != "q-new-00000000" {
		t.Errorf("job ran as %q, want the newly registered id", got)
	}
}

// A handle the service has forgotten must not be observed forever: the plugin
// falls through and registers new work.
func TestStaleRegistryHandleStartsNewWork(t *testing.T) {
	p, _ := sdkv1.NewPlugin(sdkv1.WithPluginId("plug-1"))
	var started []string
	var got string
	sdkv1.RunPipeline(p, sdkv1.Action{
		// The service no longer holds the previous query.
		Middleware:     sdkv1.Use(attachOrStart(map[string]bool{}, "q-new-00000000", &started)),
		RequestHandler: func(job sdkv1.Job) { got = job.JobId },
	}, &runtime{}, request("q-gone-8f21c4"))

	if len(started) != 1 || got != "q-new-00000000" {
		t.Errorf("stale handle: registrations=%v, job=%q; want one registration under the new id", started, got)
	}
}
