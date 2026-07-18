// Package keygen produces paste keys with three strategies (random, phonetic,
// dictionary). It uses crypto/rand rather than math/rand so keys are not
// predictable.
package keygen

import (
	"bufio"
	"crypto/rand"
	"fmt"
	"math"
	"math/big"
	"os"
	"strings"
)

// Generator creates a paste key of the requested length.
type Generator interface {
	CreateKey(length int) string
	// EntropyBits estimates the entropy in bits of a key of the requested
	// length, so callers can warn when a configuration yields guessable keys.
	EntropyBits(length int) float64
}

// randInt returns a uniformly random int in [0, n) using crypto/rand.
// n must be > 0.
func randInt(n int) int {
	v, err := rand.Int(rand.Reader, big.NewInt(int64(n)))
	if err != nil {
		// crypto/rand failures are catastrophic and effectively never happen;
		// panicking is preferable to emitting a weak/zero key.
		panic("keygen: crypto/rand failure: " + err.Error())
	}
	return int(v.Int64())
}

// Random selects characters uniformly from a fixed keyspace.
type Random struct {
	keyspace []rune
}

const defaultKeyspace = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"

// NewRandom returns a Random generator. An empty keyspace falls back to the
// default alphabet.
func NewRandom(keyspace string) *Random {
	if keyspace == "" {
		keyspace = defaultKeyspace
	}
	return &Random{keyspace: []rune(keyspace)}
}

func (g *Random) CreateKey(length int) string {
	var b strings.Builder
	for i := 0; i < length; i++ {
		b.WriteRune(g.keyspace[randInt(len(g.keyspace))])
	}
	return b.String()
}

func (g *Random) EntropyBits(length int) float64 {
	return float64(length) * math.Log2(float64(len(g.keyspace)))
}

// Phonetic builds pronounceable keys by assembling randomly chosen syllable
// templates. Each template is a short pattern of consonant ('c') and vowel ('v')
// slots; syllables are concatenated until the requested length is reached.
type Phonetic struct{}

const (
	vowels     = "aeiou"
	consonants = "bcdfghjklmnpqrstvwxyz"
)

// syllableTemplates are the pronounceable building blocks. Picking a whole
// template at random (rather than alternating single characters) yields clusters
// like "bra" or "tik" that read as natural syllables.
var syllableTemplates = []string{"cv", "cvc", "vc", "cvv", "ccv"}

func NewPhonetic() *Phonetic { return &Phonetic{} }

func (g *Phonetic) CreateKey(length int) string {
	var b strings.Builder
	b.Grow(length)
	for b.Len() < length {
		tmpl := syllableTemplates[randInt(len(syllableTemplates))]
		for i := 0; i < len(tmpl) && b.Len() < length; i++ {
			if tmpl[i] == 'c' {
				b.WriteByte(consonants[randInt(len(consonants))])
			} else {
				b.WriteByte(vowels[randInt(len(vowels))])
			}
		}
	}
	return b.String()[:length]
}

// EntropyBits approximates per-character entropy as the average over all
// syllable-template slots (consonant vs vowel choice); template boundaries are
// not observable in the flattened key, so this is a close estimate.
func (g *Phonetic) EntropyBits(length int) float64 {
	var slots int
	var bits float64
	for _, t := range syllableTemplates {
		for i := 0; i < len(t); i++ {
			if t[i] == 'c' {
				bits += math.Log2(float64(len(consonants)))
			} else {
				bits += math.Log2(float64(len(vowels)))
			}
		}
		slots += len(t)
	}
	return float64(length) * bits / float64(slots)
}

// Dictionary concatenates `length` words drawn from a word list.
type Dictionary struct {
	words []string
}

// NewDictionary loads a newline-separated word list from path.
func NewDictionary(path string) (*Dictionary, error) {
	if path == "" {
		return nil, fmt.Errorf("dictionary generator requires a word list path")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open dictionary %q: %w", path, err)
	}
	defer f.Close()

	var words []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if w := strings.TrimSpace(sc.Text()); w != "" {
			words = append(words, w)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read dictionary %q: %w", path, err)
	}
	if len(words) == 0 {
		return nil, fmt.Errorf("dictionary %q is empty", path)
	}
	return &Dictionary{words: words}, nil
}

func (g *Dictionary) CreateKey(length int) string {
	var b strings.Builder
	for i := 0; i < length; i++ {
		b.WriteString(g.words[randInt(len(g.words))])
	}
	return b.String()
}

func (g *Dictionary) EntropyBits(length int) float64 {
	return float64(length) * math.Log2(float64(len(g.words)))
}

// New builds a generator from a type name. Recognized: "random", "phonetic",
// "dictionary". An unknown or empty type defaults to phonetic. dictPath is
// required only for the dictionary type.
func New(genType, dictPath string) (Generator, error) {
	switch genType {
	case "random":
		return NewRandom(""), nil
	case "dictionary":
		return NewDictionary(dictPath)
	case "phonetic", "":
		return NewPhonetic(), nil
	default:
		return nil, fmt.Errorf("unknown key generator type %q", genType)
	}
}
