package search

const maxEmbeddingInputChars = 12000

func embeddingInput(text string) string {
	runes := []rune(text)
	if len(runes) <= maxEmbeddingInputChars {
		return text
	}
	return string(runes[:maxEmbeddingInputChars])
}
