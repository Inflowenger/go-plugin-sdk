package sdkv1

import (
	"fmt"
	"os"

	"github.com/joho/godotenv"
)

type Env struct {
	envFile string
}

func (e *Env) getEnvVar(key string) string {
	envVar, ok := os.LookupEnv(key)
	if !ok {
		fmt.Printf("Environment variable not set %s\n", key)
	}
	return envVar

}
func NewEnv(path string) *Env {
	if path == "" {
		path = ".env"
	}
	e := Env{envFile: path}
	godotenv.Load(e.envFile)

	return &e
}
