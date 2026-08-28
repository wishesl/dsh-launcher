package main

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func strp(s string) *string { return &s }

// withFavoritesFile points favoritesFilePath at a fresh temp file.
func withFavoritesFile(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	orig := favoritesFilePath
	t.Cleanup(func() { favoritesFilePath = orig })
	path := filepath.Join(dir, "favorites.json")
	favoritesFilePath = func() string { return path }
}

func TestFavoriteID(t *testing.T) {
	cases := []struct {
		name string
		f    FavoritePlugin
		want string
	}{
		{"npm preferred", FavoritePlugin{NPM: strp("@scope/pkg"), URL: "https://github.com/o/r", Name: "r"}, "@scope/pkg"},
		{"repo fallback", FavoritePlugin{URL: "https://github.com/Owner/Repo", Name: "something"}, "owner/repo"},
		{"install npm fallback", FavoritePlugin{Install: "dsh-x", Name: "X"}, "dsh-x"},
		{"name fallback", FavoritePlugin{Name: "MyPlugin"}, "myplugin"},
		{"empty", FavoritePlugin{}, ""},
	}
	for _, c := range cases {
		if got := favoriteID(c.f); got != c.want {
			t.Errorf("%s: favoriteID = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestValidateInstallSpec(t *testing.T) {
	valid := []string{
		"dsh-x", "@scope/pkg", "github:owner/repo", "github:owner/mono#path:/pkgs/a",
		"git+https://github.com/owner/repo.git",
	}
	for _, s := range valid {
		if !validateInstallSpec(s) {
			t.Errorf("validateInstallSpec(%q) should pass", s)
		}
	}
	invalid := []string{
		"", "rm -rf /", "github:owner/repo#path:/../evil", "github:onlyowner",
		"https://evil.example/x", "node-pty@1", "a b", "..", "github:o/r/../../x",
	}
	for _, s := range invalid {
		if validateInstallSpec(s) {
			t.Errorf("validateInstallSpec(%q) should fail", s)
		}
	}
}

func TestAddRemoveFavorite(t *testing.T) {
	withFavoritesFile(t)
	a := &App{}

	// catalog-source favorite (install derived server-side from url+npm)
	d := FavoriteDraft{
		Name: "modlens", Owner: "liustack", URL: "https://github.com/liustack/modlens",
		NPM: strp("@liustack/modlens"), Category: "tools", Source: "catalog",
		Description: map[string]string{"en": "a lens"},
	}
	list, err := a.AddFavorite(d)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("after add: %d favorites", len(list))
	}
	if list[0].Install != "@liustack/modlens" || list[0].ID != "@liustack/modlens" || list[0].Source != "catalog" {
		t.Fatalf("favorite = %+v", list[0])
	}

	// add same again → deduped, addedAt kept
	first := list[0].AddedAt
	list, err = a.AddFavorite(d)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("dedupe failed: %d favorites", len(list))
	}
	if list[0].AddedAt != first {
		t.Fatalf("addedAt should be kept on re-favorite (got %q want %q)", list[0].AddedAt, first)
	}

	// installed-source favorite (spec validated)
	d2 := FavoriteDraft{Name: "dsh-x", Source: "installed", Spec: "github:owner/dsh-x"}
	list, err = a.AddFavorite(d2)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("after second add: %d favorites", len(list))
	}
	if list[0].ID != "dsh-x" || list[0].Install != "github:owner/dsh-x" {
		t.Fatalf("installed favorite = %+v", list[0])
	}

	// invalid installed spec rejected
	if _, err := a.AddFavorite(FavoriteDraft{Name: "x", Source: "installed", Spec: "rm -rf /"}); err == nil {
		t.Fatal("invalid spec should be rejected")
	}
	if _, err := a.AddFavorite(FavoriteDraft{Name: "x", Source: "installed", Spec: "file:../evil"}); err == nil {
		t.Fatal("file: spec should be rejected")
	}

	// remove
	list, err = a.RemoveFavorite("@liustack/modlens")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ID != "dsh-x" {
		t.Fatalf("after remove: %+v", list)
	}

	// persisted on disk
	data, err := os.ReadFile(favoritesFilePath())
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Favorites []FavoritePlugin `json:"favorites"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Favorites) != 1 || doc.Favorites[0].ID != "dsh-x" {
		t.Fatalf("disk favorites = %+v", doc.Favorites)
	}
}

func TestShareCodeRoundTrip(t *testing.T) {
	withFavoritesFile(t)
	a := &App{}
	if _, err := a.AddFavorite(FavoriteDraft{
		Name: "modlens", URL: "https://github.com/liustack/modlens",
		NPM: strp("@liustack/modlens"), Source: "catalog",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.AddFavorite(FavoriteDraft{Name: "dsh-x", Source: "installed", Spec: "github:owner/dsh-x"}); err != nil {
		t.Fatal(err)
	}

	code, err := a.GenerateShareCode()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(code, "DSH-FAV:v1:") {
		t.Fatalf("bad prefix: %s", code)
	}

	// parse (no write) against an empty store
	withFavoritesFile(t)
	a2 := &App{}
	res, err := a2.ParseShareCode(code)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Imported) != 2 || len(res.Skipped) != 0 {
		t.Fatalf("parse = %+v", res)
	}

	// import writes
	res, err = a2.ImportShareCode(code)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Imported) != 2 {
		t.Fatalf("import = %+v", res)
	}
	if got, _ := a2.ListFavorites(); len(got) != 2 {
		t.Fatalf("after import: %d favorites", len(got))
	}

	// importing again → all skipped, no duplicates on disk
	res, err = a2.ImportShareCode(code)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Imported) != 0 || len(res.Skipped) != 2 {
		t.Fatalf("re-import = %+v", res)
	}
	if got, _ := a2.ListFavorites(); len(got) != 2 {
		t.Fatalf("re-import should not duplicate: %d favorites", len(got))
	}
}

func TestImportShareCodeErrors(t *testing.T) {
	withFavoritesFile(t)
	a := &App{}
	for _, c := range []string{"", "garbage", "DSH-FAV:v2:AAAA", "DSH-FAV:v1:!!!notbase64!!!"} {
		if _, err := a.ImportShareCode(c); err == nil {
			t.Errorf("ImportShareCode(%q) should error", c)
		}
	}

	// a valid code containing one invalid install target must skip that entry
	payload := sharePayload{V: 1, Plugins: []FavoritePlugin{
		{Name: "ok", Install: "dsh-x"},
		{Name: "evil", Install: "rm -rf /"},
	}}
	data, _ := json.Marshal(payload)
	code := shareCodePrefix + base64.RawURLEncoding.EncodeToString(data)
	res, err := a.ImportShareCode(code)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Imported) != 1 || res.Imported[0].Name != "ok" {
		t.Fatalf("import = %+v", res)
	}
}
