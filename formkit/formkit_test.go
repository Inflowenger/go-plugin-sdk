package formkit

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Inflowenger/go-plugin-sdk/sdkv1"
)

// The point of this package is that the two documents cannot drift apart, so
// most of what is worth testing is a relation between them: every control names
// a property that exists, in the order the fields were declared, and every
// button targets one too.

func demo() *Form {
	return New("Create issue").
		Describe("Opens a new issue.").
		SubmitTo("demo.validate").
		Add(
			Text("projectKey", "Project key").Required().
				Describe("e.g. OPS").
				Lookup("demo.project.resolve", "Find").Picks("demo.issue.create"),
			Text("issueKey", "Issue key").Inline(),
			TextArea("description", "Description"),
			Integer("maxResults", "Max results").Default(50).Between(1, 100),
			Enum("adjust", "Adjust estimate", "auto", "leave", "new"),
			Bool("notify", "Notify watchers").Default(true),
			List("labels", "Labels"),
		).
		Group("Advanced",
			Text("extra", "Extra fields (JSON)").Help("Merged last, so it overrides anything above."),
			Text("newEstimate", "New estimate").ShowWhen("adjust", "new"),
		)
}

func parse(t *testing.T, document string) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal([]byte(document), &out); err != nil {
		t.Fatalf("document does not parse: %v\n%s", err, document)
	}
	return out
}

// controls flattens the UI schema to the controls it renders, groups included,
// in render order.
func controls(t *testing.T, ui map[string]any) []map[string]any {
	t.Helper()
	var walk func(elements []any) []map[string]any
	walk = func(elements []any) []map[string]any {
		found := make([]map[string]any, 0, len(elements))
		for _, raw := range elements {
			element, _ := raw.(map[string]any)
			switch element["type"] {
			case "Control":
				found = append(found, element)
			default:
				nested, _ := element["elements"].([]any)
				found = append(found, walk(nested)...)
			}
		}
		return found
	}
	elements, _ := ui["elements"].([]any)
	return walk(elements)
}

func TestEveryControlNamesAProperty(t *testing.T) {
	built := demo().Build()

	schema := parse(t, built.Jsonschema)
	properties, _ := schema["properties"].(map[string]any)

	for _, control := range controls(t, parse(t, built.Jsonui)) {
		scope, _ := control["scope"].(string)
		name := strings.TrimPrefix(scope, "#/properties/")
		if _, ok := properties[name]; !ok {
			t.Errorf("control %q has no property behind it", scope)
		}
	}
	if got, want := len(controls(t, parse(t, built.Jsonui))), len(properties); got != want {
		t.Errorf("%d controls for %d properties — one of them is unreachable", got, want)
	}
}

func TestControlsKeepDeclarationOrder(t *testing.T) {
	form := demo()

	want := make([]string, 0, 9)
	for _, field := range form.Fields() {
		want = append(want, field.Name())
	}

	got := make([]string, 0, len(want))
	for _, control := range controls(t, parse(t, form.UI())) {
		got = append(got, strings.TrimPrefix(control["scope"].(string), "#/properties/"))
	}

	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("rendered %v, declared %v", got, want)
	}
}

func TestSchemaCarriesTypesRequiredAndDefaults(t *testing.T) {
	schema := parse(t, demo().Schema())

	if schema["title"] != "Create issue" || schema["description"] != "Opens a new issue." {
		t.Errorf("form heading lost: %v / %v", schema["title"], schema["description"])
	}

	required, _ := schema["required"].([]any)
	if len(required) != 1 || required[0] != "projectKey" {
		t.Errorf("required is %v, want [projectKey]", required)
	}

	properties, _ := schema["properties"].(map[string]any)
	maxResults, _ := properties["maxResults"].(map[string]any)
	if maxResults["type"] != "integer" || maxResults["default"] != float64(50) ||
		maxResults["minimum"] != float64(1) || maxResults["maximum"] != float64(100) {
		t.Errorf("maxResults is %v", maxResults)
	}

	labels, _ := properties["labels"].(map[string]any)
	items, _ := labels["items"].(map[string]any)
	if labels["type"] != "array" || items["type"] != "string" {
		t.Errorf("labels is %v, want an array of strings", labels)
	}

	adjust, _ := properties["adjust"].(map[string]any)
	values, _ := adjust["enum"].([]any)
	if len(values) != 3 || values[0] != "auto" {
		t.Errorf("adjust enum is %v", values)
	}
}

// A button that targets a property no longer on the form is the failure this
// package exists to prevent: the press succeeds, the patch names a field that
// is not there, and nothing at all happens on screen.
func TestLookupButtonsTargetRealProperties(t *testing.T) {
	form := New("Get issue").Add(
		Text("issueKey", "Issue key").Inline(),
		Text("issueSearch", "Search").
			Lookup("demo.issue.resolve", "Search").
			Into("issueKey").
			Picks("demo.issue.get"),
	)
	built := form.Build()

	properties, _ := parse(t, built.Jsonschema)["properties"].(map[string]any)

	var checked int
	for _, control := range controls(t, parse(t, built.Jsonui)) {
		ui, ok := control[uiKey].(map[string]any)
		if !ok {
			continue
		}
		checked++

		action, _ := ui["action"].(map[string]any)
		if action["name"] != "pluginFn" {
			t.Errorf("button calls %v, not the host's pluginFn bridge", action["name"])
		}
		body, _ := action["body"].(map[string]any)
		target, _ := body["targetField"].(string)
		if _, ok := properties[target]; !ok {
			t.Errorf("button writes into %q, which the form does not have", target)
		}
		if body["form"] != "demo.issue.get" {
			t.Errorf("button rebuilds %v, not its own form", body["form"])
		}
	}
	if checked != 1 {
		t.Fatalf("found %d buttons, want 1", checked)
	}
}

// A field that a *different* control fills in has to say where its messages go,
// or a lookup's answers collect at the bottom of the form, away from the value
// they are about.
func TestMessagesAndRulesReachTheControl(t *testing.T) {
	byScope := make(map[string]map[string]any)
	for _, control := range controls(t, parse(t, demo().UI())) {
		byScope[control["scope"].(string)] = control
	}

	inline, _ := byScope["#/properties/issueKey"][NotifKey].(map[string]any)
	if inline["display"] != "inline" {
		t.Errorf("issueKey does not declare where its messages appear: %v", inline)
	}

	help, _ := byScope["#/properties/extra"][NotifKey].(map[string]any)
	if help["severity"] != "help" || help["message"] == "" {
		t.Errorf("extra lost its hint: %v", help)
	}

	rule, _ := byScope["#/properties/newEstimate"]["rule"].(map[string]any)
	condition, _ := rule["condition"].(map[string]any)
	conditionSchema, _ := condition["schema"].(map[string]any)
	if rule["effect"] != "SHOW" || condition["scope"] != "#/properties/adjust" || conditionSchema["const"] != "new" {
		t.Errorf("newEstimate's visibility rule is %v", rule)
	}

	if _, ok := byScope["#/properties/description"]["options"].(map[string]any)["multi"]; !ok {
		t.Error("description is not rendered as a text area")
	}
}

func TestValidateCatchesWhatWouldNotRender(t *testing.T) {
	cases := map[string]*Form{
		"a name used twice": New("dup").Add(Text("a", "A"), Text("a", "Again")),
		"a nameless field":  New("blank").Add(Text("", "A")),
		"a value that will not marshal": New("bad").
			Add(Custom("ch", "Channel", map[string]any{"default": make(chan int)})),
	}
	for name, form := range cases {
		if err := form.Validate(); err == nil {
			t.Errorf("%s passed validation", name)
		}
	}

	if err := demo().Validate(); err != nil {
		t.Errorf("a good form failed validation: %v", err)
	}
}

// ------------------------------------------------------------ raw JSON compat --

// The picker has to work against forms this package never built, because that
// is every form written before it existed.
const handWritten = `{
  "type": "object",
  "title": "Transition issue",
  "properties": {
    "issueKey":   { "type": "string", "title": "Issue key" },
    "transition": { "type": "string", "title": "Transition", "enum": ["Done"] }
  },
  "required": ["issueKey", "transition"]
}`

func TestPickerRebuildsAHandWrittenForm(t *testing.T) {
	form := sdkv1.FormBuilder{
		Jsonschema: handWritten,
		Jsonui:     `{"type":"VerticalLayout","elements":[{"type":"Control","scope":"#/properties/transition"}]}`,
	}
	call := map[string]any{
		"issueKey":    "OPS-42",
		"transition":  "prog",
		"settings":    map[string]any{"apiToken": "secret"},
		"value":       "prog",
		"targetField": "transition",
		"form":        "jira.issue.transition",
	}

	envelope, err := Picker(form, "transition", []Option{
		{Value: "Start Progress", Label: "Start Progress → In Progress"},
		{Value: "Stop Progress", Label: "Stop Progress → To Do"},
	}, FormData(call), Info("2 transitions — pick one."))
	if err != nil {
		t.Fatalf("could not rebuild the form: %v", err)
	}

	// Envelope keys only: any other key demotes the answer to a field patch and
	// nothing re-renders.
	for key := range envelope {
		switch key {
		case "schema", "uischema", "data", NotifKey:
		default:
			t.Errorf("envelope carries %q, which would demote it to a patch", key)
		}
	}

	schema, _ := envelope["schema"].(map[string]any)
	properties, _ := schema["properties"].(map[string]any)
	transition, _ := properties["transition"].(map[string]any)

	choices, _ := transition["oneOf"].([]any)
	if len(choices) != 2 {
		t.Fatalf("transition offers %v", transition)
	}
	if first, _ := choices[0].(map[string]any); first["const"] != "Start Progress" {
		t.Errorf("first choice is %v", choices[0])
	}
	if _, ok := transition["enum"]; ok {
		t.Error("the old enum survived — intersected with oneOf it would leave nothing selectable")
	}
	if transition["title"] != "Transition" {
		t.Error("rebuilding the property dropped its title")
	}
	if required, _ := schema["required"].([]any); len(required) != 2 {
		t.Errorf("the rest of the form did not survive the rebuild: required is %v", required)
	}

	// The credentials the host attached to the call must not be promoted into
	// the form's data, where they would be saved onto the node.
	data, _ := envelope["data"].(map[string]any)
	for _, host := range []string{"settings", "value", "targetField", "form"} {
		if _, leaked := data[host]; leaked {
			t.Errorf("%q was echoed back into the form data", host)
		}
	}
	if data["issueKey"] != "OPS-42" {
		t.Errorf("the form's own fields were not echoed back: %v", data)
	}
}

func TestChooseFallsBackToText(t *testing.T) {
	options := []Option{{Value: "A-1", Label: "A-1 — one"}, {Value: "A-2", Label: "A-2 — two"}}

	answer := Choose(sdkv1.FormBuilder{Jsonschema: handWritten}, "nosuchfield", options, nil,
		Info("2 issues — pick one."))

	patch, ok := answer.(map[string]any)
	if !ok {
		t.Fatalf("fallback answered with %T", answer)
	}
	if len(patch) != 1 {
		t.Errorf("the fallback wrote fields into the form: %v", patch)
	}
	message, _ := patch[NotifKey].(Notification)
	if !strings.Contains(message.Message, "A-2 — two") {
		t.Errorf("the fallback did not list the candidates: %q", message.Message)
	}
}

func TestPatchCarriesValueAndMessage(t *testing.T) {
	patch := Success("Issue: %s", "OPS-42").Patch(map[string]any{"issueKey": "OPS-42"})

	if patch["issueKey"] != "OPS-42" {
		t.Errorf("patch lost its value: %v", patch)
	}
	message, _ := patch[NotifKey].(Notification)
	if message.Severity != "success" || message.Message != "Issue: OPS-42" {
		t.Errorf("patch message is %+v", message)
	}

	// A message on its own is a valid answer — a connection test writes nothing.
	only := Failure("cannot reach %s", "example.invalid").Patch(nil)
	if len(only) != 1 {
		t.Errorf("a message-only patch carries %v", only)
	}

	encoded, err := json.Marshal(patch)
	if err != nil {
		t.Fatalf("patch does not marshal: %v", err)
	}
	if !strings.Contains(string(encoded), `"`+NotifKey+`":{"severity":"success"`) {
		t.Errorf("patch marshals as %s", encoded)
	}
}

func TestSettingsFormBuildsWithItsHandler(t *testing.T) {
	settings := New("Connection").
		Add(
			Text("baseUrl", "Site URL").Required(),
			Secret("apiToken", "API token").Required().
				Lookup("demo.ping", "Test connection").
				Help("Press ↻ to check these values before saving."),
		).
		Settings(func(sdkv1.Request) sdkv1.Response { return sdkv1.Response{Data: map[string]any{"ok": true}} })

	if settings.SubmitHandler == nil {
		t.Fatal("settings lost its submit handler")
	}
	if !strings.Contains(settings.Jsonschema, `"required":["baseUrl","apiToken"]`) {
		t.Errorf("settings schema is %s", settings.Jsonschema)
	}
	if !strings.Contains(settings.Jsonui, `"format":"password"`) {
		t.Errorf("the token field is not masked: %s", settings.Jsonui)
	}
}
