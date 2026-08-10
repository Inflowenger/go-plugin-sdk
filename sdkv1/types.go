package sdkv1

type Command string

const (
	ProgressCommand       Command = "progress"
	ContextCurrentCommand Command = "context/current"
	ContextPathCommand    Command = "context/path"
	JobCommandCommit      Command = "commit"
	JobCommandNextTags    Command = "next_tags"
	JobCommandRequest     Command = "request/svc"
)
