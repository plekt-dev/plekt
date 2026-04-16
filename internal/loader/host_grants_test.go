package loader

import (
	"context"
	"errors"
	"testing"
)

func newTestHostGrantStore(t *testing.T) HostGrantStore {
	t.Helper()
	db := openTestRegistryDB(t)
	s, err := NewSQLiteHostGrantStore(db)
	if err != nil {
		t.Fatalf("NewSQLiteHostGrantStore: %v", err)
	}
	return s
}

func TestHostGrantStore_GrantAndList(t *testing.T) {
	s := newTestHostGrantStore(t)
	ctx := context.Background()

	g := HostGrant{PluginName: "voice", Host: "whisper:9000", GrantedBy: "op", Source: "install"}
	if err := s.Grant(ctx, g); err != nil {
		t.Fatalf("Grant: %v", err)
	}

	list, err := s.List(ctx, "voice")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 || list[0].Host != "whisper:9000" || list[0].Source != "install" {
		t.Errorf("unexpected list: %+v", list)
	}
}

func TestHostGrantStore_GrantUpsert(t *testing.T) {
	s := newTestHostGrantStore(t)
	ctx := context.Background()

	_ = s.Grant(ctx, HostGrant{PluginName: "p", Host: "h.test", GrantedBy: "a", Source: "install"})
	if err := s.Grant(ctx, HostGrant{PluginName: "p", Host: "h.test", GrantedBy: "b", Source: "operator"}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	list, _ := s.List(ctx, "p")
	if len(list) != 1 || list[0].Source != "operator" || list[0].GrantedBy != "b" {
		t.Errorf("upsert did not replace: %+v", list)
	}
}

func TestHostGrantStore_RevokeMissingAndExisting(t *testing.T) {
	s := newTestHostGrantStore(t)
	ctx := context.Background()

	if err := s.Revoke(ctx, "p", "nope.test"); !errors.Is(err, ErrHostGrantNotFound) {
		t.Errorf("want ErrHostGrantNotFound, got %v", err)
	}
	_ = s.Grant(ctx, HostGrant{PluginName: "p", Host: "h.test", GrantedBy: "a", Source: "install"})
	if err := s.Revoke(ctx, "p", "h.test"); err != nil {
		t.Errorf("Revoke existing: %v", err)
	}
}

func TestHostGrantStore_ListAll(t *testing.T) {
	s := newTestHostGrantStore(t)
	ctx := context.Background()
	_ = s.Grant(ctx, HostGrant{PluginName: "a", Host: "a.test", GrantedBy: "x", Source: "install"})
	_ = s.Grant(ctx, HostGrant{PluginName: "b", Host: "b.test", GrantedBy: "x", Source: "operator"})

	all, err := s.ListAll(ctx)
	if err != nil {
		t.Fatalf("ListAll: %v", err)
	}
	if len(all["a"]) != 1 || len(all["b"]) != 1 {
		t.Errorf("unexpected ListAll: %+v", all)
	}
}

func TestHostGrantStore_ListEmpty(t *testing.T) {
	s := newTestHostGrantStore(t)
	list, err := s.List(context.Background(), "ghost")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 0 {
		t.Errorf("want empty, got %v", list)
	}
}

func TestValidateHost(t *testing.T) {
	tests := []struct {
		host string
		ok   bool
	}{
		{"whisper", true},
		{"whisper:9000", true},
		{"api.openai.com", true},
		{"api.openai.com:443", true},
		{"my-whisper.internal:9001", true},
		{"127.0.0.1:8080", true},
		{"[::1]:8080", true},
		{"", false},
		{"http://x.test", false},
		{"x.test/path", false},
		{"x.test?q=1", false},
		{"*.evil.test", false},
		{"x.test:0", false},
		{"x.test:99999", false},
		{"-bad.test", false},
		{"bad-.test", false},
		{"bad host", false},
	}
	for _, tc := range tests {
		err := ValidateHost(tc.host)
		if (err == nil) != tc.ok {
			t.Errorf("ValidateHost(%q): got err=%v, ok=%v", tc.host, err, tc.ok)
		}
	}
}
