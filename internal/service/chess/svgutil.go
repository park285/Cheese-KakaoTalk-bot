package chess

import "bytes"

func sanitizeSVG(svg []byte) []byte {
	fixed := bytes.ReplaceAll(svg, []byte("fill:000000"), []byte("fill:#000000"))
	fixed = bytes.ReplaceAll(fixed, []byte("fill: 000000"), []byte("fill:#000000"))
	fixed = bytes.ReplaceAll(fixed, []byte("stroke: 000000"), []byte("stroke:#000000"))
	fixed = bytes.ReplaceAll(fixed, []byte("fill: #"), []byte("fill:#"))
	fixed = bytes.ReplaceAll(fixed, []byte("stroke: #"), []byte("stroke:#"))
	fixed = bytes.ReplaceAll(fixed, []byte("stop-color: #"), []byte("stop-color:#"))
	return fixed
}
