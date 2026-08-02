package sdkv1

import (
	"strings"
	"testing"
)

// The @intro and @settings handlers answer by marshalling a payload, and a
// marshal error there is invisible: the handler returns without responding, so
// the caller sees a NATS timeout and cannot tell a broken descriptor from a
// plugin that is not running.
//
// Both regressed exactly that way — @intro marshalled the `Intro` *method*
// rather than the `intro` field, and Settings carried a func field with no
// `json:"-"`. These call the same payload builders the handlers call, so a
// repeat of either failure fails here first. They need no connection: the bug
// was in the payload, not the wire.

func TestIntroMarshals(t *testing.T) {
	p := &Plugin{}
	p.Intro(PluginIntro{
		Name: "Jira", Author: "inflow Dev. Team", Version: "v0.1.0",
		Settings: &FormBuilder{Jsonschema: `{"type":"object"}`},
	})

	out, err := p.introPayload()
	if err != nil {
		t.Fatalf("@intro payload does not marshal: %v", err)
	}
	for _, want := range []string{`"name":"Jira"`, `"version":"v0.1.0"`, `"settings"`} {
		if !strings.Contains(string(out), want) {
			t.Errorf("@intro payload %s is missing %s", out, want)
		}
	}
}

func TestSettingsMarshals(t *testing.T) {
	p := &Plugin{}
	// A handler is what a plugin realistically registers, and it is the thing
	// that used to break the marshal.
	p.RequiredParams(&Settings{
		FormBuilder:   FormBuilder{SubmitTo: "settings.submit", Jsonschema: `{"type":"object"}`},
		SubmitHandler: func(Request) Response { return Response{} },
	})

	out, err := p.settingsPayload()
	if err != nil {
		t.Fatalf("@settings payload does not marshal: %v", err)
	}
	got := string(out)
	if !strings.Contains(got, `"jsonschema"`) || !strings.Contains(got, `"submit_to":"settings.submit"`) {
		t.Errorf("@settings payload %s lost the form", got)
	}
	if strings.Contains(got, "SubmitHandler") {
		t.Errorf("@settings payload %s leaks the handler", got)
	}
}

// A plugin that requires no settings must still answer, and with JSON — the
// caller has no other way to tell "asks for nothing" from "not running".
func TestSettingsPayloadWithoutRequirements(t *testing.T) {
	out, err := (&Plugin{}).settingsPayload()
	if err != nil {
		t.Fatalf("payload: %v", err)
	}
	if string(out) != "{}" {
		t.Errorf("got %q, want an empty JSON object", out)
	}
}
