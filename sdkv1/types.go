package sdkv1


type Command string
type EventType string
type LogLevel string



const (
	ProgressCommand       Command = "progress"
	StopCommand           Command = "stop"
	ContextCurrentCommand Command = "context/current"
	ContextPathCommand    Command = "context/path"
)


const (
	EventTypeLog EventType = "log"
)


const (
	LogLevelDebug LogLevel = "debug"
	LogLevelInfo  LogLevel = "info"
	LogLevelWarn  LogLevel = "warn"
	LogLevelError LogLevel = "error"
)


