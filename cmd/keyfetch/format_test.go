package main

import "testing"

func TestFormatKeyData(t *testing.T) {
	tests := []struct {
		name          string
		repoguardPath string
		data          map[string]string
		want          string
	}{
		{
			name:          "single user",
			repoguardPath: "/usr/bin/repoguard",
			data: map[string]string{
				"user1": "ssh-rsa AAAA...",
			},
			want: `command="/usr/bin/repoguard -base-dir /home/git -user user1 -log-path /home/git/log ",no-port-forwarding,no-X11-forwarding,no-agent-forwarding,no-pty ssh-rsa AAAA...` + "\n",
		},
		{
			name:          "multiple users",
			repoguardPath: "/usr/bin/repoguard",
			data: map[string]string{
				"user1": "ssh-rsa AAAA...",
				"user2": "ssh-rsa BBBB...",
			},
			want: `command="/usr/bin/repoguard -base-dir /home/git -user user1 -log-path /home/git/log ",no-port-forwarding,no-X11-forwarding,no-agent-forwarding,no-pty ssh-rsa AAAA...` + "\n" +
				`command="/usr/bin/repoguard -base-dir /home/git -user user2 -log-path /home/git/log ",no-port-forwarding,no-X11-forwarding,no-agent-forwarding,no-pty ssh-rsa BBBB...` + "\n",
		},
		{
			name:          "empty data",
			repoguardPath: "/usr/bin/repoguard",
			data:          map[string]string{},
			want:          "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatKeyData(tt.repoguardPath, tt.data); got != tt.want {
				t.Errorf("formatKeyData() = %v, want %v", got, tt.want)
			}
		})
	}
}
