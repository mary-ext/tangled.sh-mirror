package markup

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/yuin/goldmark/ast"
)

func generateCamoURL(baseURL, secret, imageURL string) string {
	h := hmac.New(sha256.New, []byte(secret))
	h.Write([]byte(imageURL))
	signature := hex.EncodeToString(h.Sum(nil))
	hexURL := hex.EncodeToString([]byte(imageURL))
	return fmt.Sprintf("%s/%s/%s", baseURL, signature, hexURL)
}

func (rctx *RenderContext) camoImageLinkTransformer(img *ast.Image) {
	// don't camo on dev
	if rctx.IsDev {
		return
	}

	dst := string(img.Destination)

	if rctx.CamoUrl != "" && rctx.CamoSecret != "" {
		img.Destination = []byte(generateCamoURL(rctx.CamoUrl, rctx.CamoSecret, dst))
	}
}
