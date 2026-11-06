package tmux_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/wrouesnel/tmux-tabbed-terminal/pkg/tmux"
)

const changedOutput = `ssh example: exit status 255: @@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@
@    WARNING: REMOTE HOST IDENTIFICATION HAS CHANGED!     @
@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@
IT IS POSSIBLE THAT SOMEONE IS DOING SOMETHING NASTY!
Someone could be eavesdropping on you right now (man-in-the-middle attack)!
It is also possible that a host key has just been changed.
The fingerprint for the ED25519 key sent by the remote host is
SHA256:Wf3m2pRGxYcXzT0nm2k4oPq9oI0bUuHUb1N1Yw4W5J8.
Please contact your system administrator.
Add correct host key in /home/u/.ssh/known_hosts to get rid of this message.
Offending ED25519 key in /home/u/.ssh/known_hosts:12
  remove with:
  ssh-keygen -f '/home/u/.ssh/known_hosts' -R '[example.com]:2222'
Host key for [example.com]:2222 has changed and you have requested strict checking.
Host key verification failed.`

func TestFindHostKeyProblem(t *testing.T) {
	for _, tc := range []struct {
		name string
		msg  string
		want *tmux.HostKeyProblem
	}{
		{"changed", changedOutput, &tmux.HostKeyProblem{
			Host: "[example.com]:2222", Changed: true, KeyType: "ED25519",
			Fingerprint: "SHA256:Wf3m2pRGxYcXzT0nm2k4oPq9oI0bUuHUb1N1Yw4W5J8",
			KnownHosts:  "/home/u/.ssh/known_hosts", Line: 12,
		}},
		{"changed, older ssh", "Offending RSA key in C:\\Users\\u/.ssh/known_hosts:3\n" +
			"Host key for db has changed and you have requested strict checking.\nHost key verification failed.",
			&tmux.HostKeyProblem{Host: "db", Changed: true, KnownHosts: "C:\\Users\\u/.ssh/known_hosts", Line: 3}},
		{"unknown", "starting the agent: No ED25519 host key is known for db.example.com and you have " +
			"requested strict checking.\r\nHost key verification failed.",
			&tmux.HostKeyProblem{Host: "db.example.com", KeyType: "ED25519"}},
		{"unknown in BatchMode", "ssh db: Host key verification failed.: exit status 255", &tmux.HostKeyProblem{}},
		{"other", "ssh: connect to host db port 22: Connection refused", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := tmux.FindHostKeyProblem(errors.New(tc.msg))
			if ok != (tc.want != nil) {
				t.Fatalf("found = %v", ok)
			}
			if ok && *got != *tc.want {
				t.Errorf("got %+v, want %+v", *got, *tc.want)
			}
		})
	}
}

func TestSplitPaths(t *testing.T) {
	for _, tc := range []struct {
		list string
		want []string
	}{
		{"/home/u/.ssh/known_hosts /home/u/.ssh/known_hosts2", []string{"/home/u/.ssh/known_hosts", "/home/u/.ssh/known_hosts2"}},
		{`C:\Users\Jo Smith/.ssh/known_hosts C:\Users\Jo Smith/.ssh/known_hosts2`,
			[]string{`C:\Users\Jo Smith/.ssh/known_hosts`, `C:\Users\Jo Smith/.ssh/known_hosts2`}},
		{"~/.ssh/kh", []string{"~/.ssh/kh"}},
	} {
		got := tmux.SplitPaths(tc.list)
		if strings.Join(got, "|") != strings.Join(tc.want, "|") {
			t.Errorf("SplitPaths(%q) = %q, want %q", tc.list, got, tc.want)
		}
	}
}
