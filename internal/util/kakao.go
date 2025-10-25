package util

import "strings"

const (
	KakaoSeeMorePadding = 500
	KakaoZeroWidthSpace = "\u200b"
)

// 카카오톡 '전체보기'용 제로폭 문자를 채워 메시지를 확장.
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

// 첫 줄에 중복된 헤더가 있으면 제거한다.
func StripLeadingHeader(text, header string) string {
	if strings.TrimSpace(text) == "" || strings.TrimSpace(header) == "" {
		return text
	}

	candidates := []string{
		header + "\r\n\r\n",
		header + "\n\n",
		header + "\r\n",
		header + "\n",
		header,
	}

	for _, candidate := range candidates {
		if strings.HasPrefix(text, candidate) {
			return strings.TrimPrefix(text, candidate)
		}
	}
	return text
}

// 헤더를 제거한 본문에 '전체보기' 지침을 붙여 패딩을 적용한다.
func ApplySeeMoreWithHeader(text, header, fallback, suffix string) string {
	if strings.TrimSpace(text) == "" {
		return text
	}

	body := StripLeadingHeader(text, header)
	instruction := strings.TrimSpace(header)
	if instruction == "" {
		instruction = strings.TrimSpace(fallback)
	} else if suffix != "" {
		instruction += suffix
	}

	if instruction == "" {
		instruction = strings.TrimSpace(fallback)
	}

	return ApplyKakaoSeeMorePadding(body, instruction)
}
