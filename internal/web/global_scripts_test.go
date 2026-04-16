package web

import (
	"errors"
	"sync"
	"testing"

	"github.com/plekt-dev/plekt/internal/loader"
)

func TestGlobalScriptRegistry_RegisterValid(t *testing.T) {
	r := NewGlobalScriptRegistry()
	if err := r.Register("voice-plugin", loader.FrontendAssets{JSFile: "global.js", CSSFile: "global.css"}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	list := r.List()
	if len(list) != 1 {
		t.Fatalf("want 1 entry, got %d", len(list))
	}
	want := "/p/voice-plugin/static/global.js"
	if list[0].URL != want {
		t.Errorf("URL = %q, want %q", list[0].URL, want)
	}
	if list[0].CSSURL != "/p/voice-plugin/static/global.css" {
		t.Errorf("CSSURL = %q", list[0].CSSURL)
	}
}

func TestGlobalScriptRegistry_RegisterInvalid(t *testing.T) {
	r := NewGlobalScriptRegistry()
	bad := []string{"", "../evil.js", "sub/dir.js", "sub\\dir.js", "/abs.js", ".hidden.js"}
	for _, name := range bad {
		err := r.Register("p", loader.FrontendAssets{JSFile: name})
		if !errors.Is(err, ErrInvalidGlobalAsset) {
			t.Errorf("Register(%q): want ErrInvalidGlobalAsset, got %v", name, err)
		}
	}
}

func TestGlobalScriptRegistry_RegisterInvalidCSS(t *testing.T) {
	r := NewGlobalScriptRegistry()
	err := r.Register("p", loader.FrontendAssets{JSFile: "ok.js", CSSFile: "../bad.css"})
	if !errors.Is(err, ErrInvalidGlobalAsset) {
		t.Errorf("want ErrInvalidGlobalAsset, got %v", err)
	}
}

func TestGlobalScriptRegistry_Unregister(t *testing.T) {
	r := NewGlobalScriptRegistry()
	_ = r.Register("p", loader.FrontendAssets{JSFile: "g.js"})
	r.Unregister("p")
	if len(r.List()) != 0 {
		t.Error("expected empty after Unregister")
	}
	r.Unregister("never") // no-op
}

func TestGlobalScriptRegistry_ListSorted(t *testing.T) {
	r := NewGlobalScriptRegistry()
	_ = r.Register("zeta", loader.FrontendAssets{JSFile: "z.js"})
	_ = r.Register("alpha", loader.FrontendAssets{JSFile: "a.js"})
	_ = r.Register("mike", loader.FrontendAssets{JSFile: "m.js"})
	list := r.List()
	if list[0].PluginName != "alpha" || list[1].PluginName != "mike" || list[2].PluginName != "zeta" {
		t.Errorf("not sorted: %+v", list)
	}
}

func TestGlobalScriptRegistry_Concurrent(t *testing.T) {
	r := NewGlobalScriptRegistry()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			name := "p"
			_ = r.Register(name, loader.FrontendAssets{JSFile: "g.js"})
			_ = r.List()
			if i%10 == 0 {
				r.Unregister(name)
			}
		}(i)
	}
	wg.Wait()
}
