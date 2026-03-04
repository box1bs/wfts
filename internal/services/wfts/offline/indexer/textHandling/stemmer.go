package textHandling

import (
	"strings"
)

type EnglishStemmer struct {
	step1aRules 	map[string]string
	step1bRules 	map[string]string
	step2Rules  	map[string]string
	step3Rules  	map[string]string
	step4Rules  	map[string]string
	stopWords 		*stopWords
	tokenizer   	*tokenizer
}

type stopWords struct {
	words map[string]struct{}
}

func newStopWords() *stopWords {
    sw := &stopWords{
        words: make(map[string]struct{}),
    }
    
    englishStops := []string{
        "a", "an", "and", "are", "as", "at", "be", "by", "for", "from",
        "has", "he", "in", "is", "it", "its", "of", "on", "that", "the",
        "to", "was", "were", "will", "with", "the", "this", "but", "they",
        "have", "had", "what", "when", "where", "who", "which", "why",
        "how", "all", "any", "both", "each", "few", "more", "most",
        "other", "some", "such", "no", "nor", "not", "only", "own",
        "same", "so", "than", "too", "very",
    }
    
    for _, word := range englishStops {
        sw.words[word] = struct{}{}
    }
    return sw
}

func (sw *stopWords) isStopWord(word string) bool {
    _, exists := sw.words[strings.ToLower(word)]
    return exists
}

func NewEnglishStemmer() *EnglishStemmer {
	return &EnglishStemmer{
		step1aRules: map[string]string{
			"sses": "ss", // possesses -> possess
			"ies":  "i",  // ponies -> poni
			"ss":   "ss", // possess -> possess
			"s":    "",   // cats -> cat
		},
		step1bRules: map[string]string{
			"eed": "ee", // agreed -> agree
			"ed":  "",   // jumped -> jump
			"ing": "",   // jumping -> jump
		},
		step2Rules: map[string]string{
			"ational": "ate",  // relational -> relate
			"tional":  "tion", // conditional -> condition
			"enci":    "ence", // valenci -> valence
			"anci":    "ance", // hesitanci -> hesitance
			"izer":    "ize",  // digitizer -> digitize
			"abli":    "able", // conformabli -> conformable
			"alli":    "al",   // radically -> radical
			"entli":   "ent",  // differently -> different
			"eli":     "e",    // namely -> name
			"ousli":   "ous",  // analogously -> analogous
			"ization": "ize",  // visualization -> visualize
			"ation":   "ate",  // predication -> predicate
			"ator":    "ate",  // operator -> operate
			"alism":   "al",   // feudalism -> feudal
			"iveness": "ive",  // decisiveness -> decisive
			"fulness": "ful",  // hopefulness -> hopeful
			"ousness": "ous",  // callousness -> callous
			"aliti":   "al",   // formality -> formal
			"iviti":   "ive",  // sensitivity -> sensitive
			"biliti":  "ble",  // sensibility -> sensible
		},
		step3Rules: map[string]string{
			"icate": "ic", // certificate -> certific
			"ative": "",   // formative -> form
			"alize": "al", // formalize -> formal
			"iciti": "ic", // electricity -> electric
			"ical":  "ic", // electrical -> electric
			"ful":   "",   // hopeful -> hope
			"ness":  "",   // goodness -> good
		},
		step4Rules: map[string]string{
			"al":    "", // revival -> reviv
			"ance":  "", // allowance -> allow
			"ence":  "", // inference -> infer
			"er":    "", // airliner -> airlin
			"ic":    "", // gyroscopic -> gyroscop
			"able":  "", // adjustable -> adjust
			"ible":  "", // defensible -> defens
			"ant":   "", // contestant -> contest
			"ement": "", // replacement -> replac
			"ment":  "", // adjustment -> adjust
			"ent":   "", // dependent -> depend
			"ion":   "", // adoption -> adopt
			"ou":    "", // homologou -> homolog
			"ism":   "", // mechanism -> mechan
			"ate":   "", // activate -> activ
			"iti":   "", // angulariti -> angular
			"ous":   "", // homologous -> homolog
			"ive":   "", // effective -> effect
			"ize":   "", // bowdlerize -> bowdler
		},
		stopWords: newStopWords(),
		tokenizer: newTokenizer(),
	}
}

func (s *EnglishStemmer) measure(word string) int {
    isVowel := func(c byte) bool {
        return strings.ContainsRune("aeiou", rune(c))
    }
    
    var m int
    var hasVowel bool
    
    for i := range len(word) {
        if isVowel(word[i]) {
            hasVowel = true
        } else if hasVowel {
            m++
            hasVowel = false
        }
    }
    
    return m
}

func (s *EnglishStemmer) TokenizeAndStem(text string) ([]string, []token, error) {
	tokens := s.tokenizer.entityTokenize(text)
	wordTokens := make([]string, 0, 32)
	stemmedTokens := make([]token, 0, 64)
	for _, t := range tokens {
		if t.Type == WORD && len(t.Value) > 0 {
			if stemmed := s.stem(t.Value); stemmed != "" {
				wordTokens = append(wordTokens, t.Value)
				stemmedTokens = append(stemmedTokens, token{Type: WORD, Value: stemmed})
			}
		} else if t.Type != UNKNOWN && t.Type != WHITESPACE {
			stemmedTokens = append(stemmedTokens, t)
		}
	}

	return wordTokens, stemmedTokens, nil
}

func (s *EnglishStemmer) stem(word string) string {
	if s.stopWords.isStopWord(word) {
		return ""
	}

    if len(word) <= 2 {
        return word
    }
    
    word = s.trimRuleSuffix(word, s.step1aRules, -1)
    word = s.trimRuleSuffix(word, s.step1bRules, 0)
	word = s.trimRuleSuffix(word, s.step2Rules, 0)
	word = s.trimRuleSuffix(word, s.step3Rules, 0)
    return s.trimRuleSuffix(word, s.step4Rules, 1)
}

func (s *EnglishStemmer) trimRuleSuffix(word string, rule map[string]string, treshold int) string {
	for suffix, replacement := range rule {
        if strings.HasSuffix(word, suffix) && s.measure(strings.TrimSuffix(word, suffix)) > treshold {
            return strings.TrimSuffix(word, suffix) + replacement
        }
    }
	return word
}