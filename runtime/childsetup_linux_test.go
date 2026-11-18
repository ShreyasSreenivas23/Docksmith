package runtime

import (
	"os"
	"testing"
)

// The opt-in setup reaches the child only when a flag asks for it, and
// the child reads it once and removes it from the command's environment.
func TestChildSetupEnv(t *testing.T) {
	defer func() { ChildLoopback, ChildDev, ChildUser = false, false, "" }()
	if env := withChildSetup([]string{"A=1"}); len(env) != 1 {
		t.Fatalf("no flags, env %v", env)
	}
	ChildLoopback, ChildDev, ChildUser = true, true, "999:998"
	env := withChildSetup([]string{"A=1"})
	if len(env) != 2 || env[1] != childSetupEnv+"=loopback,dev,user=999:998" {
		t.Fatalf("env %v", env)
	}
	os.Setenv(childSetupEnv, "loopback,dev,user=999:998")
	s, err := takeChildSetup()
	if err != nil || !s.loopback || !s.dev || !s.user || s.uid != 999 || s.gid != 998 {
		t.Fatalf("setup %+v, %v", s, err)
	}
	if _, ok := os.LookupEnv(childSetupEnv); ok {
		t.Error("setup left in the environment")
	}
	for _, bad := range []string{"user=999", "user=a:b", "user=-1:0", "teleport"} {
		os.Setenv(childSetupEnv, bad)
		if _, err := takeChildSetup(); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	os.Unsetenv(childSetupEnv)
	if s, err := takeChildSetup(); err != nil || s.loopback || s.dev || s.user {
		t.Errorf("without the variable: %+v, %v", s, err)
	}
}
