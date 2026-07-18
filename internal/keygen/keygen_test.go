package keygen

import (
	"strings"
	"testing"
)

func TestRandomKeyLengthAndAlphabet(t *testing.T) {
	g := NewRandom("")
	for _, n := range []int{1, 10, 32} {
		k := g.CreateKey(n)
		if len([]rune(k)) != n {
			t.Fatalf("length = %d, want %d", len([]rune(k)), n)
		}
		for _, r := range k {
			if !strings.ContainsRune(defaultKeyspace, r) {
				t.Fatalf("rune %q not in keyspace", r)
			}
		}
	}
}

func TestPhoneticPronounceableAndLength(t *testing.T) {
	g := NewPhonetic()
	for _, n := range []int{1, 7, 10, 33} {
		k := g.CreateKey(n)
		if len(k) != n {
			t.Fatalf("length = %d, want %d", len(k), n)
		}
		for _, r := range k {
			if !strings.ContainsRune(vowels, r) && !strings.ContainsRune(consonants, r) {
				t.Fatalf("rune %q neither vowel nor consonant", r)
			}
		}
	}
}

func TestRandomReasonablyUnique(t *testing.T) {
	g := NewRandom("")
	seen := map[string]bool{}
	for i := 0; i < 1000; i++ {
		k := g.CreateKey(10)
		if seen[k] {
			t.Fatalf("duplicate key within 1000 draws: %q", k)
		}
		seen[k] = true
	}
}

func TestEntropyBits(t *testing.T) {
	// random: 16 * log2(62) ~ 95; phonetic: 16 * ~3.4 ~ 55.
	if bits := NewRandom("").EntropyBits(16); bits < 95 || bits > 96 {
		t.Fatalf("random entropy = %f, want ~95.3", bits)
	}
	if bits := NewPhonetic().EntropyBits(16); bits < 50 || bits > 60 {
		t.Fatalf("phonetic entropy = %f, want ~55", bits)
	}
	d := &Dictionary{words: make([]string, 1024)}
	if bits := d.EntropyBits(4); bits != 40 {
		t.Fatalf("dictionary entropy = %f, want 40 (4 * log2(1024))", bits)
	}
}

func TestNewDefaultsToPhonetic(t *testing.T) {
	g, err := New("", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := g.(*Phonetic); !ok {
		t.Fatalf("default generator = %T, want *Phonetic", g)
	}
}

func TestNewUnknownType(t *testing.T) {
	if _, err := New("bogus", ""); err == nil {
		t.Fatal("expected error for unknown generator type")
	}
}
