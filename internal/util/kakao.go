package util

import "strings"

const (
	KakaoSeeMorePadding = 500
	KakaoZeroWidthSpace = "\u200b"
)

func ApplyKakaoSeeMorePadding(text, instruction string) string {
	if strings.TrimSpace(text) == "" {
		return text
	}

	message := strings.TrimSpace(instruction)

	var builder strings.Builder
	builder.Grow(len(text) + KakaoSeeMorePadding + len(message) + 2)

	if message != "" {
		builder.WriteString(message)
	}
	builder.WriteString(strings.Repeat(KakaoZeroWidthSpace, KakaoSeeMorePadding))
	if !strings.HasPrefix(text, "\n") {
		builder.WriteByte('\n')
	}
	builder.WriteString(text)

	return builder.String()
}
