# Form builder — action & settings UIs

Every action a plugin exposes can carry a **form**: the dialog a user fills in on
the canvas to configure that node. Settings (onboarding) forms work the same way.
Forms are declarative — the plugin ships JSON, the front end renders it.

## The `FormBuilder`

```go
type FormBuilder struct {
    SubmitTo   string `json:"submit_to"`  // optional meta function for live validation
    Jsonui     string `json:"jsonui"`     // the UI Schema (layout / widgets)
    Jsonschema string `json:"jsonschema"` // the JSON Schema (data model / validation)
}
```

- **`Jsonschema`** — a standard [JSON Schema](https://json-schema.org) describing
  the data your action expects. This defines fields, types, required-ness, and
  validation. It is exactly the shape that arrives back as the `body` of the
  request (see [protocol-inflowv1.md](protocol-inflowv1.md)).
- **`Jsonui`** — a **UI Schema** describing how to lay the fields out (groups,
  ordering, widgets, labels).
- **`SubmitTo`** — optionally, the name of a **meta function** to call for live
  validation as the user edits (see below).

## Rendering: JSON Forms + `x-inflow-ui`

Forms are rendered by [**JSON Forms**](https://jsonforms.io) — the schema + UI
schema pattern. Inflowenger ships a Vue 3 renderer set,
[`@inflowenger/inflow-ui`](../../inflow-vue/inflow-inspector/packages/inflow-ui),
that extends JSON Forms with custom `x-inflow-ui` renderers (custom controls,
enums, layouts, one-of, and action buttons) tailored to the canvas. Because it is
plain JSON Schema + UI Schema, any JSON Forms tooling can author or preview a form.

A minimal action form:

```go
schema := `{
  "type": "object",
  "properties": {
    "url":    { "type": "string", "title": "URL", "format": "uri" },
    "method": { "type": "string", "enum": ["GET", "POST", "PUT", "DELETE"] }
  },
  "required": ["url", "method"]
}`

ui := `{
  "type": "VerticalLayout",
  "elements": [
    { "type": "Control", "scope": "#/properties/url" },
    { "type": "Control", "scope": "#/properties/method" }
  ]
}`

p.AddAction(sdkv1.Action{
    Method:         "http.call",
    Title:          "HTTP Call",
    Description:    "Perform an outbound HTTP request",
    Icon:           sdkv1.Icon{Icon: "mdi-web"},
    Form:           sdkv1.FormBuilder{Jsonschema: schema, Jsonui: ui},
    RequestHandler: httpHandler,
})
```

The runtime fetches this form on demand from
`inflow.v1.<PLUGIN_ID>.http.call.@form`. What the user enters becomes the `body`
of the execution request.

## Settings (onboarding) forms

`RequiredParams` registers a plugin-level settings form plus a handler for when the
user submits it — useful for credentials or config the plugin needs before any
action runs:

```go
p.RequiredParams(&sdkv1.Settings{
    FormBuilder: sdkv1.FormBuilder{
        Jsonschema: settingsSchema,
        Jsonui:     settingsUi,
        SubmitTo:   "_settings.config.submit", // default if left blank
    },
    SubmitHandler: func(r sdkv1.Request) sdkv1.Response {
        // validate / persist the submitted settings
        return sdkv1.Response{Data: map[string]any{"ok": true}}
    },
})
```

The form is served on `inflow.v1.<PLUGIN_ID>.@settings`; submissions are handled on
`inflow.v1.<PLUGIN_ID>.<SubmitTo>` and answered with a `Response`.

`PluginIntro.Settings` is a related, lighter option: a `FormBuilder` attached
directly to the intro, usable as an onboarding stage shown when the plugin is first
added.

## Live validation with meta functions

Set `SubmitTo` on an action's `FormBuilder` to the name of a **meta function** — a
lightweight request/reply handler (not a job) the front end can call as the user
types, e.g. to check that a URL is reachable or a name is unique. Meta functions
answer on `inflow.v1.<PLUGIN_ID>.<method>` and return a `Response`:

```go
type Meta struct {
    Method         string
    RequestHandler func(sdkv1.Request) sdkv1.Response
}
```

Unlike an action, a meta function is synchronous request/reply with no job,
progress, or context access — it exists to give the form immediate feedback.

> **Status.** The `Meta` type and its subscription wiring
> ([`metaFunchandler`](../sdkv1/inflowV1.go)) are in place, and `Start()` serves
> any registered meta functions. A public method for registering them on the
> plugin is not exported yet, so treat meta functions as a defined-but-emerging
> part of the API. The settings **submit** path (`Settings.SubmitHandler`) is the
> fully wired equivalent today.
