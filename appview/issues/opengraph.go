package issues

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"log"
	"net/http"

	"tangled.org/core/appview/models"
	"tangled.org/core/appview/ogcard"
)

func (rp *Issues) drawIssueSummaryCard(issue *models.Issue, repo *models.Repo, commentCount int, ownerHandle string) (*ogcard.Card, error) {
	width, height := ogcard.DefaultSize()
	mainCard, err := ogcard.NewCard(width, height)
	if err != nil {
		return nil, err
	}

	// Split: content area (75%) and status/stats area (25%)
	contentCard, statsArea := mainCard.Split(false, 75)

	// Add padding to content
	contentCard.SetMargin(50)

	// Split content horizontally: main content (80%) and avatar area (20%)
	mainContent, avatarArea := contentCard.Split(true, 80)

	// Add margin to main content like repo card
	mainContent.SetMargin(10)

	// Use full main content area for repo name and title
	bounds := mainContent.Img.Bounds()
	startX := bounds.Min.X + mainContent.Margin
	startY := bounds.Min.Y + mainContent.Margin

	// Draw full repository name at top (owner/repo format)
	var repoOwner string
	owner, err := rp.idResolver.ResolveIdent(context.Background(), repo.Did)
	if err != nil {
		repoOwner = repo.Did
	} else {
		repoOwner = "@" + owner.Handle.String()
	}

	fullRepoName := repoOwner + " / " + repo.Name
	if len(fullRepoName) > 60 {
		fullRepoName = fullRepoName[:60] + "…"
	}

	grayColor := color.RGBA{88, 96, 105, 255}
	err = mainContent.DrawTextAt(fullRepoName, startX, startY, grayColor, 36, ogcard.Top, ogcard.Left)
	if err != nil {
		return nil, err
	}

	// Draw issue title below repo name with wrapping
	titleY := startY + 60
	titleX := startX

	// Truncate title if too long
	issueTitle := issue.Title
	maxTitleLength := 80
	if len(issueTitle) > maxTitleLength {
		issueTitle = issueTitle[:maxTitleLength] + "…"
	}

	// Create a temporary card for the title area to enable wrapping
	titleBounds := mainContent.Img.Bounds()
	titleWidth := titleBounds.Dx() - (startX - titleBounds.Min.X) - 20   // Leave some margin
	titleHeight := titleBounds.Dy() - (titleY - titleBounds.Min.Y) - 100 // Leave space for issue ID

	titleRect := image.Rect(titleX, titleY, titleX+titleWidth, titleY+titleHeight)
	titleCard := &ogcard.Card{
		Img:    mainContent.Img.SubImage(titleRect).(*image.RGBA),
		Font:   mainContent.Font,
		Margin: 0,
	}

	// Draw wrapped title
	lines, err := titleCard.DrawText(issueTitle, color.Black, 54, ogcard.Top, ogcard.Left)
	if err != nil {
		return nil, err
	}

	// Calculate where title ends (number of lines * line height)
	lineHeight := 60 // Approximate line height for 54pt font
	titleEndY := titleY + (len(lines) * lineHeight) + 10

	// Draw issue ID in gray below the title
	issueIdText := fmt.Sprintf("#%d", issue.IssueId)
	err = mainContent.DrawTextAt(issueIdText, startX, titleEndY, grayColor, 54, ogcard.Top, ogcard.Left)
	if err != nil {
		return nil, err
	}

	// Get issue author handle (needed for avatar and metadata)
	var authorHandle string
	author, err := rp.idResolver.ResolveIdent(context.Background(), issue.Did)
	if err != nil {
		authorHandle = issue.Did
	} else {
		authorHandle = "@" + author.Handle.String()
	}

	// Draw avatar circle on the right side
	avatarBounds := avatarArea.Img.Bounds()
	avatarSize := min(avatarBounds.Dx(), avatarBounds.Dy()) - 20 // Leave some margin
	if avatarSize > 220 {
		avatarSize = 220
	}
	avatarX := avatarBounds.Min.X + (avatarBounds.Dx() / 2) - (avatarSize / 2)
	avatarY := avatarBounds.Min.Y + 20

	// Get avatar URL for issue author
	avatarURL := rp.pages.AvatarUrl(authorHandle, "256")
	err = avatarArea.DrawCircularExternalImage(avatarURL, avatarX, avatarY, avatarSize)
	if err != nil {
		log.Printf("failed to draw avatar (non-fatal): %v", err)
	}

	// Split stats area: left side for status/comments (80%), right side for dolly (20%)
	statusCommentsArea, dollyArea := statsArea.Split(true, 80)

	// Draw status and comment count in status/comments area
	statsBounds := statusCommentsArea.Img.Bounds()
	statsX := statsBounds.Min.X + 60 // left padding
	statsY := statsBounds.Min.Y

	iconColor := color.RGBA{88, 96, 105, 255}
	iconSize := 36
	textSize := 36.0
	labelSize := 28.0
	iconBaselineOffset := int(textSize) / 2

	// Draw status (open/closed) with colored icon and text
	var statusIcon string
	var statusText string
	var statusBgColor color.RGBA

	if issue.Open {
		statusIcon = "static/icons/circle-dot.svg"
		statusText = "open"
		statusBgColor = color.RGBA{34, 139, 34, 255} // green
	} else {
		statusIcon = "static/icons/circle-dot.svg"
		statusText = "closed"
		statusBgColor = color.RGBA{52, 58, 64, 255} // dark gray
	}

	badgeIconSize := 36

	// Draw icon with status color (no background)
	err = statusCommentsArea.DrawSVGIcon(statusIcon, statsX, statsY+iconBaselineOffset-badgeIconSize/2+5, badgeIconSize, statusBgColor)
	if err != nil {
		log.Printf("failed to draw status icon: %v", err)
	}

	// Draw text with status color (no background)
	textX := statsX + badgeIconSize + 12
	badgeTextSize := 32.0
	err = statusCommentsArea.DrawTextAt(statusText, textX, statsY+iconBaselineOffset, statusBgColor, badgeTextSize, ogcard.Middle, ogcard.Left)
	if err != nil {
		log.Printf("failed to draw status text: %v", err)
	}

	statusTextWidth := len(statusText) * 20
	currentX := statsX + badgeIconSize + 12 + statusTextWidth + 50

	// Draw comment count
	err = statusCommentsArea.DrawSVGIcon("static/icons/message-square.svg", currentX, statsY+iconBaselineOffset-iconSize/2+5, iconSize, iconColor)
	if err != nil {
		log.Printf("failed to draw comment icon: %v", err)
	}

	currentX += iconSize + 15
	commentText := fmt.Sprintf("%d comments", commentCount)
	if commentCount == 1 {
		commentText = "1 comment"
	}
	err = statusCommentsArea.DrawTextAt(commentText, currentX, statsY+iconBaselineOffset, iconColor, textSize, ogcard.Middle, ogcard.Left)
	if err != nil {
		log.Printf("failed to draw comment text: %v", err)
	}

	// Draw dolly logo on the right side
	dollyBounds := dollyArea.Img.Bounds()
	dollySize := 90
	dollyX := dollyBounds.Min.X + (dollyBounds.Dx() / 2) - (dollySize / 2)
	dollyY := statsY + iconBaselineOffset - dollySize/2 + 25
	dollyColor := color.RGBA{180, 180, 180, 255} // light gray
	err = dollyArea.DrawSVGIcon("templates/fragments/dolly/silhouette.svg", dollyX, dollyY, dollySize, dollyColor)
	if err != nil {
		log.Printf("dolly silhouette not available (this is ok): %v", err)
	}

	// Draw "opened by @author" and date at the bottom with more spacing
	labelY := statsY + iconSize + 30

	// Format the opened date
	openedDate := issue.Created.Format("Jan 2, 2006")
	metaText := fmt.Sprintf("opened by %s · %s", authorHandle, openedDate)

	err = statusCommentsArea.DrawTextAt(metaText, statsX, labelY, iconColor, labelSize, ogcard.Top, ogcard.Left)
	if err != nil {
		log.Printf("failed to draw metadata: %v", err)
	}

	return mainCard, nil
}

func (rp *Issues) IssueOpenGraphSummary(w http.ResponseWriter, r *http.Request) {
	f, err := rp.repoResolver.Resolve(r)
	if err != nil {
		log.Println("failed to get repo and knot", err)
		return
	}

	issue, ok := r.Context().Value("issue").(*models.Issue)
	if !ok {
		log.Println("issue not found in context")
		http.Error(w, "issue not found", http.StatusNotFound)
		return
	}

	// Get comment count
	commentCount := len(issue.Comments)

	// Get owner handle for avatar
	var ownerHandle string
	owner, err := rp.idResolver.ResolveIdent(r.Context(), f.Repo.Did)
	if err != nil {
		ownerHandle = f.Repo.Did
	} else {
		ownerHandle = "@" + owner.Handle.String()
	}

	card, err := rp.drawIssueSummaryCard(issue, &f.Repo, commentCount, ownerHandle)
	if err != nil {
		log.Println("failed to draw issue summary card", err)
		http.Error(w, "failed to draw issue summary card", http.StatusInternalServerError)
		return
	}

	var imageBuffer bytes.Buffer
	err = png.Encode(&imageBuffer, card.Img)
	if err != nil {
		log.Println("failed to encode issue summary card", err)
		http.Error(w, "failed to encode issue summary card", http.StatusInternalServerError)
		return
	}

	imageBytes := imageBuffer.Bytes()

	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "public, max-age=3600") // 1 hour
	w.WriteHeader(http.StatusOK)
	_, err = w.Write(imageBytes)
	if err != nil {
		log.Println("failed to write issue summary card", err)
		return
	}
}
