package static

import (
	"embed"
	"mime"
)

//go:embed all:css all:js all:icons favicon.ico favicon-16x16.png favicon-32x32.png apple-touch-icon.png android-chrome-192x192.png android-chrome-512x512.png site.webmanifest
var Assets embed.FS

func init() {
	// Windows registry may override Go's built-in MIME types with wrong
	// values (e.g. .css → text/plain). Force correct types globally so
	// http.FileServer uses them via mime.TypeByExtension().
	// See https://github.com/golang/go/issues/32350
	mime.AddExtensionType(".css", "text/css; charset=utf-8")
	mime.AddExtensionType(".js", "text/javascript; charset=utf-8")
	mime.AddExtensionType(".json", "application/json; charset=utf-8")
	mime.AddExtensionType(".svg", "image/svg+xml")
	mime.AddExtensionType(".webmanifest", "application/manifest+json")
	mime.AddExtensionType(".woff", "font/woff")
	mime.AddExtensionType(".woff2", "font/woff2")
	mime.AddExtensionType(".ico", "image/x-icon")
	mime.AddExtensionType(".png", "image/png")
	mime.AddExtensionType(".wasm", "application/wasm")
}
