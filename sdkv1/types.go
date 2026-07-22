package sdkv1

type Command string

const (
	ProgressCommand       Command = "progress"
	StopCommand           Command = "stop"
	ContextCurrentCommand Command = "context/current"
	ContextPathCommand    Command = "context/path"
	JobCommandCommit      Command = "commit"
	JobCommandNextTags    Command = "next_tags"
)
