package mapping

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/leqwin/monloader/internal/config"
	"github.com/leqwin/monloader/internal/gdl"
)

// liveDownloadOne runs the real gallery-dl through the managed-config download
// path and returns the mapper plus the first downloaded item's sidecar
// metadata. The managed config sets tags:true for the flat-tag families so
// their per-category tags appear. A machine without gallery-dl skips (CI
// installs the pinned version). The cases below assert metadata shape, not
// exact posts - live results vary - so a gallery-dl bump that renamed a field
// drops the categorized tags or the rating here, which the pure mapping tests
// on fixed input cannot see. They download one item to a temp dir and read its
// metadata, so the content rating is incidental.
func liveDownloadOne(t *testing.T, url string) (*Mapper, map[string]any) {
	t.Helper()
	cfg := config.Default()
	cfg.GalleryDL.BinaryPath = "gallery-dl"
	// A managed config writes the .json sidecars the download pass reads; no
	// archive so the post is always fetched rather than skipped on a re-run.
	dir := t.TempDir()
	cfg.GalleryDL.ConfigPath = filepath.Join(dir, "gallery-dl.json")
	cfg.GalleryDL.ArchivePath = ""
	mapper, err := New(config.NewProvider(cfg))
	if err != nil {
		t.Fatalf("mapper: %v", err)
	}
	if err := gdl.WriteManagedConfig(cfg, mapper.FlatTagSites()); err != nil {
		t.Fatalf("WriteManagedConfig: %v", err)
	}
	tool := gdl.New(cfg, mapper.FlatTagSites())
	if tool.Version(context.Background()) == "" {
		t.Skip("real gallery-dl not available")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	downloaded, err := tool.Download(ctx, url, "1-1", filepath.Join(dir, "work"), false, nil, false)
	if err != nil {
		t.Fatalf("real Download: %v", err)
	}
	if len(downloaded) == 0 {
		t.Fatal("expected at least one downloaded file with a sidecar")
	}
	return mapper, downloaded[0].Meta
}

// hasCategoryTags reports whether meta carries any tags_<category> field. Its
// absence on a site that categorizes is the fingerprint of a gallery-dl bump
// renaming or dropping those fields: the mapper would then flatten every tag
// into general.
func hasCategoryTags(meta map[string]any) bool {
	for k := range meta {
		if strings.HasPrefix(k, "tags_") {
			return true
		}
	}
	return false
}

// TestLiveMappingDanbooru maps a live danbooru post. danbooru categorizes
// natively, so the sidecar carries tags_<category> fields without tags:true.
func TestLiveMappingDanbooru(t *testing.T) {
	mapper, meta := liveDownloadOne(t, "https://danbooru.donmai.us/posts?tags=landscape+rating:general")
	if !hasCategoryTags(meta) {
		t.Error("danbooru sidecar carried no tags_<category> fields; gallery-dl's tag shape may have changed")
	}
	pf := mapper.Map(meta)
	if pf.Source != "danbooru" {
		t.Errorf("source = %q, want danbooru", pf.Source)
	}
	if !strings.HasPrefix(pf.URL, "https://danbooru.donmai.us/posts/") {
		t.Errorf("url = %q, want the danbooru post-url template", pf.URL)
	}
	if len(pf.Tags) == 0 {
		t.Error("mapped post produced no tags")
	}
	if pf.Rating != RatingGeneral {
		t.Errorf("rating = %q, want general (the search filtered rating:general)", pf.Rating)
	}
}

// TestLiveMappingDirectlink maps a live bare media URL through gallery-dl's
// directlink pseudo-extractor. It has no booru behind it, so the host is the
// source and the file URL is rebuilt from the parts gallery-dl exposes; the
// fixture is the extractor's own example. A gallery-dl bump renaming the
// domain/path parts would change the rebuilt URL here, which the fixed-input
// test cannot see.
func TestLiveMappingDirectlink(t *testing.T) {
	const fileURL = "https://en.wikipedia.org/static/images/project-logos/enwiki.png"
	mapper, meta := liveDownloadOne(t, fileURL)
	pf := mapper.Map(meta)
	if pf.Source != "en.wikipedia.org" {
		t.Errorf("source = %q, want the file host", pf.Source)
	}
	if pf.URL != fileURL {
		t.Errorf("url = %q, want the file URL %q", pf.URL, fileURL)
	}
}

// TestLiveMappingSafebooru maps a live safebooru post. safebooru.org is a
// flat-tag gelbooru_v02 site: it categorizes only with tags:true, which the
// managed config sets. This pins that path - a bump breaking the tags option
// would drop the per-category tags here while danbooru (native) stayed green.
func TestLiveMappingSafebooru(t *testing.T) {
	mapper, meta := liveDownloadOne(t, "https://safebooru.org/index.php?page=post&s=list&tags=1girl")
	if !hasCategoryTags(meta) {
		t.Error("safebooru sidecar carried no tags_<category> fields; the tags:true path or gallery-dl's tag shape may have changed")
	}
	pf := mapper.Map(meta)
	if pf.Source != "safebooru" {
		t.Errorf("source = %q, want safebooru", pf.Source)
	}
	if !strings.HasPrefix(pf.URL, "https://safebooru.org/index.php?page=post&s=view&id=") {
		t.Errorf("url = %q, want the safebooru post-url template", pf.URL)
	}
	if len(pf.Tags) == 0 {
		t.Error("mapped post produced no tags")
	}
	// The family maps s -> general and the profile pins its stale q -> general,
	// so any post maps to general.
	if pf.Rating != RatingGeneral {
		t.Errorf("rating = %q, want general (s and stale q both map to general)", pf.Rating)
	}
}

// TestLiveMappingE621 maps a live e621 post. e621 categorizes natively with tag
// classes the other families lack (species, lore, contributor) and overloads
// the rating letter: the search pins rating:s, which is "safe" here (general),
// not danbooru's "sensitive".
func TestLiveMappingE621(t *testing.T) {
	mapper, meta := liveDownloadOne(t, "https://e621.net/posts?tags=wolf+rating:s")
	if !hasCategoryTags(meta) {
		t.Error("e621 sidecar carried no tags_<category> fields; gallery-dl's tag shape may have changed")
	}
	pf := mapper.Map(meta)
	if pf.Source != "e621" {
		t.Errorf("source = %q, want e621", pf.Source)
	}
	if !strings.HasPrefix(pf.URL, "https://e621.net/posts/") {
		t.Errorf("url = %q, want the e621 post-url template", pf.URL)
	}
	if len(pf.Tags) == 0 {
		t.Error("mapped post produced no tags")
	}
	if pf.Rating != RatingGeneral {
		t.Errorf("rating = %q, want general (e621 s = safe)", pf.Rating)
	}
}

// TestLiveMappingMoebooru maps a live konachan post. konachan is a moebooru
// site - a second flat-tag family on a different extractor than gelbooru - that
// categorizes only with tags:true. rating:s is the family's safe -> general.
func TestLiveMappingMoebooru(t *testing.T) {
	mapper, meta := liveDownloadOne(t, "https://konachan.com/post?tags=landscape+rating:s")
	if !hasCategoryTags(meta) {
		t.Error("konachan sidecar carried no tags_<category> fields; the tags:true path or gallery-dl's tag shape may have changed")
	}
	pf := mapper.Map(meta)
	if pf.Source != "konachan" {
		t.Errorf("source = %q, want konachan", pf.Source)
	}
	if !strings.HasPrefix(pf.URL, "https://konachan.com/post/show/") {
		t.Errorf("url = %q, want the konachan post-url template", pf.URL)
	}
	if len(pf.Tags) == 0 {
		t.Error("mapped post produced no tags")
	}
	if pf.Rating != RatingGeneral {
		t.Errorf("rating = %q, want general (moebooru s = safe)", pf.Rating)
	}
}

// TestLiveMappingPhilomena maps a live derpibooru post. Philomena boorus carry
// no rating field and no tags_<category> shape: the rating is one of the tags
// and the flat tags route by namespace prefix. The search pins the "safe" tag,
// which is lifted to the general rating rather than kept as a content tag.
func TestLiveMappingPhilomena(t *testing.T) {
	mapper, meta := liveDownloadOne(t, "https://derpibooru.org/search?q=safe")
	pf := mapper.Map(meta)
	if pf.Source != "derpibooru" {
		t.Errorf("source = %q, want derpibooru", pf.Source)
	}
	if !strings.HasPrefix(pf.URL, "https://derpibooru.org/images/") {
		t.Errorf("url = %q, want the derpibooru post-url template", pf.URL)
	}
	if len(pf.Tags) == 0 {
		t.Error("mapped post produced no tags")
	}
	if pf.Rating != RatingGeneral {
		t.Errorf("rating = %q, want general (lifted from the safe tag)", pf.Rating)
	}
	if slices.Contains(pf.Tags, "safe") {
		t.Error("the rating tag should be lifted, not kept as a content tag")
	}
}

// TestLiveMappingManga maps a live mangadex chapter. A manga/comic source has no
// tags_<category> shape; its categorized fields (artist, author, group) are
// separate lists the manga-kind path folds into the artist category. A chapter
// URL keeps --range 1-1 to one page rather than a whole title's chapters. The
// chapter is a fixed fixture; if mangadex removes it, point this at another.
func TestLiveMappingManga(t *testing.T) {
	mapper, meta := liveDownloadOne(t, "https://mangadex.org/chapter/bdb2b04b-6120-448e-b16c-8706fa37b526")
	pf := mapper.Map(meta)
	if pf.Source != "mangadex" {
		t.Errorf("source = %q, want mangadex", pf.Source)
	}
	if len(pf.Tags) == 0 {
		t.Error("mapped chapter produced no tags")
	}
	// artist / author / group all fold to the artist category; a manga-kind
	// source that stopped folding them would drop every credited name.
	if !slices.ContainsFunc(pf.Tags, func(s string) bool { return strings.HasPrefix(s, "artist:") }) {
		t.Errorf("expected a folded artist tag from the manga fields, got %v", pf.Tags)
	}
}
