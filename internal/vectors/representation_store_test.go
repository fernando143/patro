package vectors

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fernando143/patro/internal/embed"
)

func v2Identity() embed.RepresentationIdentity {
	return embed.RepresentationIdentity{
		SchemaVersion: 2, Backend: "cybertron", ModelID: "mini", ModelVersion: "cybertron-spago-v1",
		ModelWeightsSHA256: strings.Repeat("a", 64), TokenizerSHA256: strings.Repeat("b", 64),
		ChunkerVersion: embed.ChunkerVersion, NormalizationVersion: embed.NormalizationVersion, Dimension: 2,
	}
}

func v2Representation(id string, sourceHash string) embed.Representation {
	identity := v2Identity()
	return embed.Representation{
		SchemaVersion: 2, DocumentID: id, SourceHash: sourceHash, Backend: identity.Backend,
		ModelID: identity.ModelID, ModelVersion: identity.ModelVersion, ModelWeightsSHA256: identity.ModelWeightsSHA256,
		TokenizerSHA256: identity.TokenizerSHA256, ChunkerVersion: identity.ChunkerVersion,
		NormalizationVersion: identity.NormalizationVersion, RepresentationFingerprint: embed.Fingerprint(identity),
		Dimension: 2, Chunks: []embed.Chunk{{Kind: "content", Ordinal: 0, TokenCount: 1, SourceStartRune: 0, SourceEndRune: 4, Vector: []float32{1, 0}}},
	}
}

func TestV2RoundTripIsCanonicalAndOrderIndependent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "topics.json")
	identity := v2Identity()
	entries := []embed.Representation{v2Representation("zeta", strings.Repeat("c", 64)), v2Representation("alpha", strings.Repeat("d", 64))}
	if err := WriteV2(path, identity, entries); err != nil {
		t.Fatal(err)
	}
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	reversed := []embed.Representation{entries[1], entries[0]}
	if err := WriteV2(path, identity, reversed); err != nil {
		t.Fatal(err)
	}
	second, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Fatal("v2 bytes changed when entries were shuffled")
	}
	loaded, needsRebuild, err := LoadV2(path, identity)
	if err != nil || needsRebuild || len(loaded) != 2 {
		t.Fatalf("LoadV2() = entries=%d needs=%v err=%v", len(loaded), needsRebuild, err)
	}
}

func TestV2RejectsAliasesMalformedDataAndLegacyModelVersion(t *testing.T) {
	dir := t.TempDir()
	identity := v2Identity()
	path := filepath.Join(dir, "topics.json")
	if err := os.WriteFile(path, []byte(`{"schema_version":2,"backend":"cybertron","weights_sha256":"bad"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := LoadV2(path, identity); err == nil {
		t.Fatal("LoadV2() accepted alias field")
	}
	legacy := filepath.Join(dir, "legacy.json")
	if err := os.WriteFile(legacy, []byte(`{"backend":"cybertron","dim":384,"model_version":"cybertron","entries":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, needsRebuild, err := LoadV2(legacy, identity); err != nil || !needsRebuild {
		t.Fatalf("LoadV2(legacy) = needs=%v err=%v, want invalidation", needsRebuild, err)
	}
}

func TestV2RejectsInvalidVectorAndIdentityMismatch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "topics.json")
	identity := v2Identity()
	bad := v2Representation("bad", strings.Repeat("e", 64))
	bad.Chunks[0].Vector = []float32{2, 0}
	if err := WriteV2(path, identity, []embed.Representation{bad}); err == nil {
		t.Fatal("WriteV2() accepted a non-unit vector")
	}
	goodPath := filepath.Join(dir, "good.json")
	if err := WriteV2(goodPath, identity, []embed.Representation{v2Representation("ok", strings.Repeat("f", 64))}); err != nil {
		t.Fatal(err)
	}
	other := identity
	other.ModelVersion = "cybertron"
	if _, needsRebuild, err := LoadV2(goodPath, other); err != nil || !needsRebuild {
		t.Fatalf("LoadV2(identity mismatch) = needs=%v err=%v, want invalidation", needsRebuild, err)
	}
}
