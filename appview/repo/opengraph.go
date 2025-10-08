package repo

import (
	"bytes"
	"context"
	"encoding/hex"
	"fmt"
	"image/color"
	"image/png"
	"log"
	"net/http"
	"sort"
	"strings"

	"github.com/go-enry/go-enry/v2"
	"tangled.org/core/appview/db"
	"tangled.org/core/appview/models"
	"tangled.org/core/appview/repo/ogcard"
	"tangled.org/core/types"
)

func (rp *Repo) drawRepoSummaryCard(repo *models.Repo, languageStats []types.RepoLanguageDetails) (*ogcard.Card, error) {
	width, height := ogcard.DefaultSize()
	mainCard, err := ogcard.NewCard(width, height)
	if err != nil {
		return nil, err
	}

	// Split: content area (75%) and language bar + icons (25%)
	contentCard, bottomArea := mainCard.Split(false, 75)

	// Add padding to content
	contentCard.SetMargin(30)

	// Split content horizontally: main content (80%) and avatar area (20%)
	mainContent, avatarArea := contentCard.Split(true, 80)

	// Split main content: 50% for name/description, 50% for spacing
	topSection, _ := mainContent.Split(false, 50)

	// Split top section: 40% for repo name, 60% for description
	repoNameCard, descriptionCard := topSection.Split(false, 50)

	// Draw repo name with owner in regular and repo name in bold
	repoNameCard.SetMargin(10)
	var ownerHandle string
	owner, err := rp.idResolver.ResolveIdent(context.Background(), repo.Did)
	if err != nil {
		ownerHandle = repo.Did
	} else {
		ownerHandle = "@" + owner.Handle.String()
	}

	// Draw repo name with wrapping support
	repoNameCard.SetMargin(10)
	bounds := repoNameCard.Img.Bounds()
	startX := bounds.Min.X + repoNameCard.Margin
	startY := bounds.Min.Y + repoNameCard.Margin
	currentX := startX
	textColor := color.RGBA{88, 96, 105, 255}

	// Draw owner handle in gray
	ownerWidth, err := repoNameCard.DrawTextAtWithWidth(ownerHandle, currentX, startY, textColor, 54, ogcard.Top, ogcard.Left)
	if err != nil {
		return nil, err
	}
	currentX += ownerWidth

	// Draw separator
	sepWidth, err := repoNameCard.DrawTextAtWithWidth(" / ", currentX, startY, textColor, 54, ogcard.Top, ogcard.Left)
	if err != nil {
		return nil, err
	}
	currentX += sepWidth

	// Draw repo name in bold
	_, err = repoNameCard.DrawBoldText(repo.Name, currentX, startY, color.Black, 54, ogcard.Top, ogcard.Left)
	if err != nil {
		return nil, err
	}

	// Draw description (DrawText handles multi-line wrapping automatically)
	descriptionCard.SetMargin(10)
	description := repo.Description
	if len(description) > 70 {
		description = description[:70] + "…"
	}

	_, err = descriptionCard.DrawText(description, color.RGBA{88, 96, 105, 255}, 36, ogcard.Top, ogcard.Left)
	if err != nil {
		log.Printf("failed to draw description: %v", err)
		return nil, err
	}

	// Draw avatar circle on the right side
	avatarBounds := avatarArea.Img.Bounds()
	avatarSize := min(avatarBounds.Dx(), avatarBounds.Dy()) - 20 // Leave some margin
	if avatarSize > 220 {
		avatarSize = 220
	}
	avatarX := avatarBounds.Min.X + (avatarBounds.Dx() / 2) - (avatarSize / 2)
	avatarY := avatarBounds.Min.Y + 20

	// Get avatar URL and draw it
	avatarURL := rp.pages.AvatarUrl(ownerHandle, "256")
	err = avatarArea.DrawCircularExternalImage(avatarURL, avatarX, avatarY, avatarSize)
	if err != nil {
		log.Printf("failed to draw avatar (non-fatal): %v", err)
	}

	// Split bottom area: icons area (65%) and language bar (35%)
	iconsArea, languageBarCard := bottomArea.Split(false, 75)

	// Split icons area: left side for stats (80%), right side for dolly (20%)
	statsArea, dollyArea := iconsArea.Split(true, 80)

	// Draw stats with icons in the stats area
	starsText := repo.RepoStats.StarCount
	issuesText := repo.RepoStats.IssueCount.Open
	pullRequestsText := repo.RepoStats.PullCount.Open

	iconColor := color.RGBA{88, 96, 105, 255}
	iconSize := 36
	textSize := 36.0

	// Position stats in the middle of the stats area
	statsBounds := statsArea.Img.Bounds()
	statsX := statsBounds.Min.X + 60 // left padding
	statsY := statsBounds.Min.Y
	currentX = statsX
	labelSize := 22.0
	// Draw star icon, count, and label
	// Align icon baseline with text baseline
	iconBaselineOffset := int(textSize) / 2
	err = statsArea.DrawSVGIcon("static/icons/star.svg", currentX, statsY+iconBaselineOffset-iconSize/2, iconSize, iconColor)
	if err != nil {
		log.Printf("failed to draw star icon: %v", err)
	}
	starIconX := currentX
	currentX += iconSize + 15

	starText := fmt.Sprintf("%d", starsText)
	err = statsArea.DrawTextAt(starText, currentX, statsY+iconBaselineOffset, iconColor, textSize, ogcard.Middle, ogcard.Left)
	if err != nil {
		log.Printf("failed to draw star text: %v", err)
	}
	starTextWidth := len(starText) * 20
	starGroupWidth := iconSize + 15 + starTextWidth

	// Draw "stars" label below and centered under the icon+text group
	labelY := statsY + iconSize + 15
	labelX := starIconX + starGroupWidth/2
	err = iconsArea.DrawTextAt("stars", labelX, labelY, iconColor, labelSize, ogcard.Top, ogcard.Center)
	if err != nil {
		log.Printf("failed to draw stars label: %v", err)
	}

	currentX += starTextWidth + 50

	// Draw issues icon, count, and label
	issueStartX := currentX
	err = statsArea.DrawSVGIcon("static/icons/circle-dot.svg", currentX, statsY+iconBaselineOffset-iconSize/2, iconSize, iconColor)
	if err != nil {
		log.Printf("failed to draw circle-dot icon: %v", err)
	}
	currentX += iconSize + 15

	issueText := fmt.Sprintf("%d", issuesText)
	err = statsArea.DrawTextAt(issueText, currentX, statsY+iconBaselineOffset, iconColor, textSize, ogcard.Middle, ogcard.Left)
	if err != nil {
		log.Printf("failed to draw issue text: %v", err)
	}
	issueTextWidth := len(issueText) * 20
	issueGroupWidth := iconSize + 15 + issueTextWidth

	// Draw "issues" label below and centered under the icon+text group
	labelX = issueStartX + issueGroupWidth/2
	err = iconsArea.DrawTextAt("issues", labelX, labelY, iconColor, labelSize, ogcard.Top, ogcard.Center)
	if err != nil {
		log.Printf("failed to draw issues label: %v", err)
	}

	currentX += issueTextWidth + 50

	// Draw pull request icon, count, and label
	prStartX := currentX
	err = statsArea.DrawSVGIcon("static/icons/git-pull-request.svg", currentX, statsY+iconBaselineOffset-iconSize/2, iconSize, iconColor)
	if err != nil {
		log.Printf("failed to draw git-pull-request icon: %v", err)
	}
	currentX += iconSize + 15

	prText := fmt.Sprintf("%d", pullRequestsText)
	err = statsArea.DrawTextAt(prText, currentX, statsY+iconBaselineOffset, iconColor, textSize, ogcard.Middle, ogcard.Left)
	if err != nil {
		log.Printf("failed to draw PR text: %v", err)
	}
	prTextWidth := len(prText) * 20
	prGroupWidth := iconSize + 15 + prTextWidth

	// Draw "pulls" label below and centered under the icon+text group
	labelX = prStartX + prGroupWidth/2
	err = iconsArea.DrawTextAt("pulls", labelX, labelY, iconColor, labelSize, ogcard.Top, ogcard.Center)
	if err != nil {
		log.Printf("failed to draw pulls label: %v", err)
	}

	dollyBounds := dollyArea.Img.Bounds()
	dollySize := 90
	dollyX := dollyBounds.Min.X + (dollyBounds.Dx() / 2) - (dollySize / 2)
	dollyY := statsY + iconBaselineOffset - dollySize/2 + 25
	dollyColor := color.RGBA{180, 180, 180, 255} // light gray
	err = dollyArea.DrawSVGIcon("templates/fragments/dolly/silhouette.svg", dollyX, dollyY, dollySize, dollyColor)
	if err != nil {
		log.Printf("dolly silhouette not available (this is ok): %v", err)
	}

	// Draw language bar at bottom
	err = drawLanguagesCard(languageBarCard, languageStats)
	if err != nil {
		log.Printf("failed to draw language bar: %v", err)
		return nil, err
	}

	return mainCard, nil
}

// hexToColor converts a hex color to a go color
func hexToColor(colorStr string) (*color.RGBA, error) {
	colorStr = strings.TrimLeft(colorStr, "#")

	b, err := hex.DecodeString(colorStr)
	if err != nil {
		return nil, err
	}

	if len(b) < 3 {
		return nil, fmt.Errorf("expected at least 3 bytes from DecodeString, got %d", len(b))
	}

	clr := color.RGBA{b[0], b[1], b[2], 255}

	return &clr, nil
}

func drawLanguagesCard(card *ogcard.Card, languageStats []types.RepoLanguageDetails) error {
	bounds := card.Img.Bounds()
	cardWidth := bounds.Dx()

	if len(languageStats) == 0 {
		// Draw a light gray bar if no languages detected
		card.DrawRect(bounds.Min.X, bounds.Min.Y, bounds.Max.X, bounds.Max.Y, color.RGBA{225, 228, 232, 255})
		return nil
	}

	// Limit to top 5 languages for the visual bar
	displayLanguages := languageStats
	if len(displayLanguages) > 5 {
		displayLanguages = displayLanguages[:5]
	}

	currentX := bounds.Min.X

	for _, lang := range displayLanguages {
		var langColor *color.RGBA
		var err error

		if lang.Color != "" {
			langColor, err = hexToColor(lang.Color)
			if err != nil {
				// Fallback to a default color
				langColor = &color.RGBA{149, 157, 165, 255}
			}
		} else {
			// Default color if no color specified
			langColor = &color.RGBA{149, 157, 165, 255}
		}

		langWidth := float32(cardWidth) * (lang.Percentage / 100)
		card.DrawRect(currentX, bounds.Min.Y, currentX+int(langWidth), bounds.Max.Y, langColor)
		currentX += int(langWidth)
	}

	// Fill remaining space with the last color (if any gap due to rounding)
	if currentX < bounds.Max.X && len(displayLanguages) > 0 {
		lastLang := displayLanguages[len(displayLanguages)-1]
		var lastColor *color.RGBA
		var err error

		if lastLang.Color != "" {
			lastColor, err = hexToColor(lastLang.Color)
			if err != nil {
				lastColor = &color.RGBA{149, 157, 165, 255}
			}
		} else {
			lastColor = &color.RGBA{149, 157, 165, 255}
		}
		card.DrawRect(currentX, bounds.Min.Y, bounds.Max.X, bounds.Max.Y, lastColor)
	}

	return nil
}

func (rp *Repo) RepoOpenGraphSummary(w http.ResponseWriter, r *http.Request) {
	f, err := rp.repoResolver.Resolve(r)
	if err != nil {
		log.Println("failed to get repo and knot", err)
		return
	}

	// Get language stats directly from database
	var languageStats []types.RepoLanguageDetails
	langs, err := db.GetRepoLanguages(
		rp.db,
		db.FilterEq("repo_at", f.RepoAt()),
		db.FilterEq("is_default_ref", 1),
	)
	if err != nil {
		log.Printf("failed to get language stats from db: %v", err)
		// non-fatal, continue without language stats
	} else if len(langs) > 0 {
		var total int64
		for _, l := range langs {
			total += l.Bytes
		}

		for _, l := range langs {
			percentage := float32(l.Bytes) / float32(total) * 100
			color := enry.GetColor(l.Language)
			languageStats = append(languageStats, types.RepoLanguageDetails{
				Name:       l.Language,
				Percentage: percentage,
				Color:      color,
			})
		}

		sort.Slice(languageStats, func(i, j int) bool {
			if languageStats[i].Name == enry.OtherLanguage {
				return false
			}
			if languageStats[j].Name == enry.OtherLanguage {
				return true
			}
			if languageStats[i].Percentage != languageStats[j].Percentage {
				return languageStats[i].Percentage > languageStats[j].Percentage
			}
			return languageStats[i].Name < languageStats[j].Name
		})
	}

	card, err := rp.drawRepoSummaryCard(&f.Repo, languageStats)
	if err != nil {
		log.Println("failed to draw repo summary card", err)
		http.Error(w, "failed to draw repo summary card", http.StatusInternalServerError)
		return
	}

	var imageBuffer bytes.Buffer
	err = png.Encode(&imageBuffer, card.Img)
	if err != nil {
		log.Println("failed to encode repo summary card", err)
		http.Error(w, "failed to encode repo summary card", http.StatusInternalServerError)
		return
	}

	imageBytes := imageBuffer.Bytes()

	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "public, max-age=3600") // 1 hour
	w.WriteHeader(http.StatusOK)
	_, err = w.Write(imageBytes)
	if err != nil {
		log.Println("failed to write repo summary card", err)
		return
	}
}
