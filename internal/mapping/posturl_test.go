package mapping

import "testing"

func TestPostURL(t *testing.T) {
	m := newMapper(t, nil)
	if got := m.PostURL(map[string]any{"category": "danbooru", "id": float64(11474309)}); got != "https://danbooru.donmai.us/posts/11474309" {
		t.Errorf("PostURL = %q, want the danbooru post page", got)
	}
	// sankaku and idolcomplex use alphanumeric ids on their modern /posts/{id}
	// scheme, not the legacy /post/show/ path.
	if got := m.PostURL(map[string]any{"category": "sankaku", "id": "vkr3EelGKMZ"}); got != "https://sankaku.app/posts/vkr3EelGKMZ" {
		t.Errorf("PostURL = %q, want the sankaku post page", got)
	}
	if got := m.PostURL(map[string]any{"category": "idolcomplex", "id": "6ea43jg5M3v"}); got != "https://www.idolcomplex.com/posts/6ea43jg5M3v" {
		t.Errorf("PostURL = %q, want the idolcomplex post page", got)
	}
	// A source with no profile template (the generic fallback) yields no link.
	if got := m.PostURL(map[string]any{"category": "wackybooru", "id": "7"}); got != "" {
		t.Errorf("PostURL = %q, want empty for an unmapped source", got)
	}
	// A directlink has no template; its canonical link is the file URL rebuilt
	// from the metadata parts.
	if got := m.PostURL(map[string]any{
		"category": "directlink", "domain": "img.example.com",
		"path": "art/2024", "filename": "picture", "extension": "jpg",
	}); got != "https://img.example.com/art/2024/picture.jpg" {
		t.Errorf("PostURL = %q, want the rebuilt directlink file URL", got)
	}
}
