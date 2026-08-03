package formkit

import "fmt"

// uiKey is the UI Schema extension that hangs a button off a control. It is
// read by the Inflowenger JSON Forms renderer set; a renderer that does not
// know it ignores it and draws a plain field, which is the correct fallback —
// every field a button fills stays typable.
const uiKey = "x-inflow-ui"

// NotifKey is the reserved key a message travels under, both in a form (on a
// control, as the messages that field shows from the moment it renders) and in
// a meta function's answer (as what the lookup has to say about what it just
// did).
//
// The host lifts it out of an answer, so it is not form data: no schema
// declares it and no action receives it. That is the whole reason it exists —
// the alternative is a read-only "status" property that every form has to carry
// and every reader of the form's data has to know to ignore.
const NotifKey = "x-inflow-notif"

// Notification is one thing to say about a field. The host decides where it
// appears — inline under the control, a toast, a dialog — so this side says
// only what happened and how much it matters.
//
// Field is optional: a message answering a button defaults to the field that
// button targets, which is what almost every message is about.
type Notification struct {
	Severity string `json:"severity,omitempty"`
	Message  string `json:"message,omitempty"`
	Field    string `json:"field,omitempty"`
	Display  string `json:"display,omitempty"`
}

// Info is guidance: what to fill in first, what the button will do next. Not a
// failure — the user has simply not got there yet.
func Info(format string, args ...any) Notification { return say("info", format, args...) }

// Success confirms what a lookup found, next to the value it just wrote.
func Success(format string, args ...any) Notification { return say("success", format, args...) }

// Warning is a search that ran and found nothing. The term is usually the
// problem, so it is worth more than an Info and less than a Failure.
func Warning(format string, args ...any) Notification { return say("warning", format, args...) }

// Failure is the remote service or the connection saying no — the severity a
// host is most likely to promote into a toast or a dialog of its own.
func Failure(format string, args ...any) Notification { return say("error", format, args...) }

// Help is a standing hint the field carries from the moment it renders, as
// opposed to something a lookup reports later.
func Help(format string, args ...any) Notification { return say("help", format, args...) }

func say(severity, format string, args ...any) Notification {
	return Notification{Severity: severity, Message: fmt.Sprintf(format, args...)}
}

// About points the message at a named field instead of the one the button
// targets.
func (n Notification) About(field string) Notification {
	n.Field = field
	return n
}

// Patch is the answer a button handler returns when it resolved a value: the
// fields to write into the open form, plus this message.
//
// Keys are absolute leaf paths — patching a nested object replaces it wholesale
// rather than merging into it.
//
//	return formkit.Success("Issue: %s", key).Patch(map[string]any{"issueKey": key})
//
// A message on its own is a valid answer: a connection test writes nothing, and
// saying so is the entire point of the button.
//
//	return formkit.Failure("cannot reach %s: %s", site, err).Patch(nil)
func (n Notification) Patch(values map[string]any) map[string]any {
	patch := make(map[string]any, len(values)+1)
	for key, value := range values {
		patch[key] = value
	}
	patch[NotifKey] = n
	return patch
}

// Option is one candidate a lookup matched, or one entry of a [Choice]: the
// value the API needs, and the label a human recognises.
type Option struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

func oneOf(options []Option) []any {
	choices := make([]any, 0, len(options))
	for _, option := range options {
		label := option.Label
		if label == "" {
			label = option.Value
		}
		choices = append(choices, map[string]any{"const": option.Value, "title": label})
	}
	return choices
}

// ----------------------------------------------------------- lookup buttons --

// Lookup hangs a button off the field that calls one of the plugin's meta
// functions and patches the answer back into the open form.
//
// `fn` is the meta method to call. The host posts the form as it currently
// stands, plus the settings profile the node is bound to, plus the contents of
// this control as `value` — so a handler reads the term the user typed from
// `value` and everything else it needs from the form's own fields.
//
// The handler answers with a patch ([Notification.Patch]) when it resolved one
// value, or with a rebuilt form ([Picker]) when the user has to choose. It does
// not answer with an sdkv1.Response: that envelope would land in the form as
// two fields called "data" and "error".
//
// The button assists; it never gates. The field stays typable whether or not
// anyone ever presses it, because a flow may be authored by someone without
// credentials for the target system, or fed from an upstream node.
func (f *Field) Lookup(fn, label string) *Field {
	f.inflowUI = map[string]any{
		"action": map[string]any{
			"name": "pluginFn",
			"fn":   fn,
			"body": map[string]any{"targetField": f.name},
		},
		"button": map[string]any{"position": "append", "label": label, "icon": "↻"},
	}
	return f
}

// Into points the answer at another property, for a button that fills in a
// field other than the one it sits on — a search box whose result belongs in
// the key field beside it.
//
// A separate search box is worth the extra property whenever the value is one
// people also paste in: searching then never overwrites a key that is already
// set, and the term stays on screen while they pick from the matches.
func (f *Field) Into(target string) *Field {
	f.body()["targetField"] = target
	return f
}

// Picks names the action whose form is rebuilt when the lookup finds more than
// one candidate (see [Picker]).
//
// Without it an ambiguous lookup can only list what it found as text; with it,
// the target field becomes a drop-down of exactly those candidates.
func (f *Field) Picks(method string) *Field {
	f.body()["form"] = method
	return f
}

// Send adds a static value to the body every press of this button posts, for
// the handlers that serve several fields and need to be told which one they are
// answering.
func (f *Field) Send(key string, value any) *Field {
	f.body()[key] = value
	return f
}

// Button overrides the look of the lookup button: where it sits relative to the
// control ("append", "prepend") and the icon on it.
func (f *Field) Button(position, icon string) *Field {
	button, _ := f.inflowUI["button"].(map[string]any)
	if button == nil {
		return f
	}
	if position != "" {
		button["position"] = position
	}
	if icon != "" {
		button["icon"] = icon
	}
	return f
}

// body reaches the static body this field's button posts, creating the button
// scaffolding if Lookup has not been called yet so the order of the chain does
// not matter.
func (f *Field) body() map[string]any {
	if f.inflowUI == nil {
		f.Lookup("", "")
	}
	action, _ := f.inflowUI["action"].(map[string]any)
	body, _ := action["body"].(map[string]any)
	return body
}

// ---------------------------------------------------------------- messages --

// Help attaches a standing hint to the field — shown from the moment the form
// renders, unlike anything a lookup reports later.
func (f *Field) Help(format string, args ...any) *Field {
	f.notifs = append(f.notifs, Help(format, args...))
	return f
}

// Inline marks the field as the place messages about it are shown.
//
// Every lookup needs one somewhere. A meta function has no other channel to the
// user — the host applies its answer to the form and surfaces nothing but
// transport failures itself — so a lookup whose answers have nowhere to appear
// reads as a button that does nothing.
//
// A field carrying a button already is one: the host renders messages at any
// control it renders. This is for the fields a *different* control fills in —
// the key that the search box writes into — whose messages would otherwise
// collect at the bottom of the form, away from the value they are about.
func (f *Field) Inline() *Field {
	f.notifs = append(f.notifs, Notification{Display: "inline"})
	return f
}

// Says attaches a message built by hand, for a severity or a target this
// package's helpers do not cover.
func (f *Field) Says(n Notification) *Field {
	f.notifs = append(f.notifs, n)
	return f
}
