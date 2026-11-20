package gitcache

import "testing"

// RedactUserinfo replaces the user part of the authority of a ref or URL,
// and keeps an SSH user name that has no password.
func TestRedactUserinfo(t *testing.T) {
	tests := []struct{ in, want string }{
		{"user:pw@github.com/org/repo", "***@github.com/org/repo"},
		{"user:pw@github.com:443/org/repo", "***@github.com:443/org/repo"},
		{"tok@github.com/org/repo", "***@github.com/org/repo"},
		{"a@b@github.com/org/repo", "***@github.com/org/repo"},
		{"https://user:pw@github.com/org/repo.git", "https://***@github.com/org/repo.git"},
		{"https://tok@github.com/org/repo.git", "https://***@github.com/org/repo.git"},
		{"HTTPS://user:pw@github.com/x", "HTTPS://***@github.com/x"},
		{"https://user:pw@github.com?x@y", "https://***@github.com?x@y"},
		{"git://user:pw@host/org/repo", "git://***@host/org/repo"},
		{"ssh://user:pw@host/org/repo", "ssh://***@host/org/repo"},
		// A second scheme: the user part of the inner URL.
		{"https://http://user:pw@github.com/org/repo.git", "https://http://***@github.com/org/repo.git"},
		{"https://HTTPS://user:pw@github.com/org/repo.git", "https://HTTPS://***@github.com/org/repo.git"},
		{"git://http://user:pw@github.com/org/repo", "git://http://***@github.com/org/repo"},
		{"https://ftp://user:p@ss@h.example/x.git", "https://ftp://***@h.example/x.git"},
		{"https://ssh://git@github.com/org/repo.git", "https://ssh://git@github.com/org/repo.git"},
		// No secret: an SSH user name, an @ in the path, no user part.
		{"ssh://git@github.com/org/repo", "ssh://git@github.com/org/repo"},
		{"ssh://git@github.com/org/repo@v1", "ssh://git@github.com/org/repo@v1"},
		{"git@github.com:org/repo", "git@github.com:org/repo"},
		{"git@github.com:org/repo@v1", "git@github.com:org/repo@v1"},
		{"https://github.com/org/repo@v1.git", "https://github.com/org/repo@v1.git"},
		{"github.com/org/repo@v1", "github.com/org/repo@v1"},
		{"/home/me@work/repo", "/home/me@work/repo"},
		{"c0ffee", "c0ffee"},
		{"--", "--"},
		{"", ""},
	}
	for _, tt := range tests {
		if got := RedactUserinfo(tt.in); got != tt.want {
			t.Errorf("RedactUserinfo(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
