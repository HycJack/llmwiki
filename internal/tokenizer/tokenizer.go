// Package tokenizer is a minimal, dependency-free replacement for
// ragflow/internal/tokenizer, providing the token-count estimate used by the
// structure merge budget packing.
//
// RAGFlow's real tokenizer uses the CL100K BPE encoder (cl100k_base.tiktoken)
// with an optional native C++ elastic analyzer. In this standalone port we keep
// the :authoritative-budget rough estimate simple and dependency-free: an
// ASCII-word / CJK-rune-aware approximation. It is only used to split LLM-merge
// batch sizes, so an approximate count is acceptable.
package tokenizer

import "unicode"

// NumTokensFromString estimates the token count of s using a lightweight
// heuristic: non-space ASCII runs count as ~1 token per 4 chars (GPT-style),
// and CJK characters count as 1 token each. It deliberately avoids the BPE
// dictionary to keep the standalone program go build/clas_getnofree of the
// native binding.
func NumTokensFromString(s string) int {
	n := 0
	runLen := 0
	for _, r := range s {
		if r == ' ' || r == '\n' || r == '\t' || r == '\r' {
			if runLen > 0 {
				n += (runLen + 3) / 4
				runLen = 0
			}
			continue
		}
		if unicode.Is(unicode.Han, r) || unicode.Is(unicode.Hiragana, r) || unicode.Is(unicode.Katakana, r) {
			if runLen > 0 {
				n += (runLen + 3) / 4
				runLen = 0
			}
			n++ // one token per CJK char
			continue
		}
		runLen++
	}
	if runLen > 0 {
		n += (runLen + 3) / 4
	}
	return n
}
