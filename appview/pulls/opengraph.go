package pulls

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"log"
	"net/http"

	"tangled.org/core/appview/db"
	"tangled.org/core/appview/models"
	"tangled.org/core/appview/ogcard"
	"tangled.org/core/patchutil"
	"tangled.org/core/types"
)

func (s *Pulls) drawPullSummaryCard(pull *models.Pull, repo *models.Repo, commentCount int, diffStats types.DiffStat, filesChanged int) (*ogcard.Card, error) {
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

	// Add margin to main content
	mainContent.SetMargin(10)

	// Use full main content area for repo name and title
	bounds := mainContent.Img.Bounds()
	startX := bounds.Min.X + mainContent.Margin
	startY := bounds.Min.Y + mainContent.Margin

	// Draw full repository name at top (owner/repo format)
	var repoOwner string
	owner, err := s.idResolver.ResolveIdent(context.Background(), repo.Did)
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

	// Draw pull request title below repo name with wrapping
	titleY := startY + 60
	titleX := startX

	// Truncate title if too long
	pullTitle := pull.Title
	maxTitleLength := 80
	if len(pullTitle) > maxTitleLength {
		pullTitle = pullTitle[:maxTitleLength] + "…"
	}

	// Create a temporary card for the title area to enable wrapping
	titleBounds := mainContent.Img.Bounds()
	titleWidth := titleBounds.Dx() - (startX - titleBounds.Min.X) - 20   // Leave some margin
	titleHeight := titleBounds.Dy() - (titleY - titleBounds.Min.Y) - 100 // Leave space for pull ID

	titleRect := image.Rect(titleX, titleY, titleX+titleWidth, titleY+titleHeight)
	titleCard := &ogcard.Card{
		Img:    mainContent.Img.SubImage(titleRect).(*image.RGBA),
		Font:   mainContent.Font,
		Margin: 0,
	}

	// Draw wrapped title
	lines, err := titleCard.DrawText(pullTitle, color.Black, 54, ogcard.Top, ogcard.Left)
	if err != nil {
		return nil, err
	}

	// Calculate where title ends (number of lines * line height)
	lineHeight := 60 // Approximate line height for 54pt font
	titleEndY := titleY + (len(lines) * lineHeight) + 10

	// Draw pull ID in gray below the title
	pullIdText := fmt.Sprintf("#%d", pull.PullId)
	err = mainContent.DrawTextAt(pullIdText, startX, titleEndY, grayColor, 54, ogcard.Top, ogcard.Left)
	if err != nil {
		return nil, err
	}

	// Get pull author handle (needed for avatar and metadata)
	var authorHandle string
	author, err := s.idResolver.ResolveIdent(context.Background(), pull.OwnerDid)
	if err != nil {
		authorHandle = pull.OwnerDid
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

	// Get avatar URL for pull author
	avatarURL := s.pages.AvatarUrl(authorHandle, "256")
	err = avatarArea.DrawCircularExternalImage(avatarURL, avatarX, avatarY, avatarSize)
	if err != nil {
		log.Printf("failed to draw avatar (non-fatal): %v", err)
	}

	// Split stats area: left side for status/stats (80%), right side for dolly (20%)
	statusStatsArea, dollyArea := statsArea.Split(true, 80)

	// Draw status and stats
	statsBounds := statusStatsArea.Img.Bounds()
	statsX := statsBounds.Min.X + 60 // left padding
	statsY := statsBounds.Min.Y

	iconColor := color.RGBA{88, 96, 105, 255}
	iconSize := 36
	textSize := 36.0
	labelSize := 28.0
	iconBaselineOffset := int(textSize) / 2

	// Draw status (open/merged/closed) with colored icon and text
	var statusIcon string
	var statusText string
	var statusColor color.RGBA

	if pull.State.IsOpen() {
		statusIcon = "git-pull-request"
		statusText = "open"
		statusColor = color.RGBA{34, 139, 34, 255} // green
	} else if pull.State.IsMerged() {
		statusIcon = "git-merge"
		statusText = "merged"
		statusColor = color.RGBA{138, 43, 226, 255} // purple
	} else {
		statusIcon = "git-pull-request-closed"
		statusText = "closed"
		statusColor = color.RGBA{128, 128, 128, 255} // gray
	}

	statusIconSize := 36

	// Draw icon with status color
	err = statusStatsArea.DrawLucideIcon(statusIcon, statsX, statsY+iconBaselineOffset-statusIconSize/2+5, statusIconSize, statusColor)
	if err != nil {
		log.Printf("failed to draw status icon: %v", err)
	}

	// Draw text with status color
	textX := statsX + statusIconSize + 12
	statusTextSize := 32.0
	err = statusStatsArea.DrawTextAt(statusText, textX, statsY+iconBaselineOffset, statusColor, statusTextSize, ogcard.Middle, ogcard.Left)
	if err != nil {
		log.Printf("failed to draw status text: %v", err)
	}

	statusTextWidth := len(statusText) * 20
	currentX := statsX + statusIconSize + 12 + statusTextWidth + 40

	// Draw comment count
	err = statusStatsArea.DrawLucideIcon("message-square", currentX, statsY+iconBaselineOffset-iconSize/2+5, iconSize, iconColor)
	if err != nil {
		log.Printf("failed to draw comment icon: %v", err)
	}

	currentX += iconSize + 15
	commentText := fmt.Sprintf("%d comments", commentCount)
	if commentCount == 1 {
		commentText = "1 comment"
	}
	err = statusStatsArea.DrawTextAt(commentText, currentX, statsY+iconBaselineOffset, iconColor, textSize, ogcard.Middle, ogcard.Left)
	if err != nil {
		log.Printf("failed to draw comment text: %v", err)
	}

	commentTextWidth := len(commentText) * 20
	currentX += commentTextWidth + 40

	// Draw files changed
	err = statusStatsArea.DrawLucideIcon("static/icons/file-diff", currentX, statsY+iconBaselineOffset-iconSize/2+5, iconSize, iconColor)
	if err != nil {
		log.Printf("failed to draw file diff icon: %v", err)
	}

	currentX += iconSize + 15
	filesText := fmt.Sprintf("%d files", filesChanged)
	if filesChanged == 1 {
		filesText = "1 file"
	}
	err = statusStatsArea.DrawTextAt(filesText, currentX, statsY+iconBaselineOffset, iconColor, textSize, ogcard.Middle, ogcard.Left)
	if err != nil {
		log.Printf("failed to draw files text: %v", err)
	}

	filesTextWidth := len(filesText) * 20
	currentX += filesTextWidth

	// Draw additions (green +)
	greenColor := color.RGBA{34, 139, 34, 255}
	additionsText := fmt.Sprintf("+%d", diffStats.Insertions)
	err = statusStatsArea.DrawTextAt(additionsText, currentX, statsY+iconBaselineOffset, greenColor, textSize, ogcard.Middle, ogcard.Left)
	if err != nil {
		log.Printf("failed to draw additions text: %v", err)
	}

	additionsTextWidth := len(additionsText) * 20
	currentX += additionsTextWidth + 30

	// Draw deletions (red -) right next to additions
	redColor := color.RGBA{220, 20, 60, 255}
	deletionsText := fmt.Sprintf("-%d", diffStats.Deletions)
	err = statusStatsArea.DrawTextAt(deletionsText, currentX, statsY+iconBaselineOffset, redColor, textSize, ogcard.Middle, ogcard.Left)
	if err != nil {
		log.Printf("failed to draw deletions text: %v", err)
	}

	// Draw dolly logo on the right side
	dollyBounds := dollyArea.Img.Bounds()
	dollySize := 90
	dollyX := dollyBounds.Min.X + (dollyBounds.Dx() / 2) - (dollySize / 2)
	dollyY := statsY + iconBaselineOffset - dollySize/2 + 25
	dollyColor := color.RGBA{180, 180, 180, 255} // light gray
	err = dollyArea.DrawDollySilhouette(dollyX, dollyY, dollySize, dollyColor)
	if err != nil {
		log.Printf("dolly silhouette not available (this is ok): %v", err)
	}

	// Draw "opened by @author" and date at the bottom with more spacing
	labelY := statsY + iconSize + 30

	// Format the opened date
	openedDate := pull.Created.Format("Jan 2, 2006")
	metaText := fmt.Sprintf("opened by %s · %s", authorHandle, openedDate)

	err = statusStatsArea.DrawTextAt(metaText, statsX, labelY, iconColor, labelSize, ogcard.Top, ogcard.Left)
	if err != nil {
		log.Printf("failed to draw metadata: %v", err)
	}

	return mainCard, nil
}

func (s *Pulls) PullOpenGraphSummary(w http.ResponseWriter, r *http.Request) {
	f, err := s.repoResolver.Resolve(r)
	if err != nil {
		log.Println("failed to get repo and knot", err)
		return
	}

	pull, ok := r.Context().Value("pull").(*models.Pull)
	if !ok {
		log.Println("pull not found in context")
		http.Error(w, "pull not found", http.StatusNotFound)
		return
	}

	// Get comment count from database
	comments, err := db.GetPullComments(s.db, db.FilterEq("pull_id", pull.ID))
	if err != nil {
		log.Printf("failed to get pull comments: %v", err)
	}
	commentCount := len(comments)

	// Calculate diff stats from latest submission using patchutil
	var diffStats types.DiffStat
	filesChanged := 0
	if len(pull.Submissions) > 0 {
		latestSubmission := pull.Submissions[len(pull.Submissions)-1]
		niceDiff := patchutil.AsNiceDiff(latestSubmission.Patch, pull.TargetBranch)
		diffStats.Insertions = int64(niceDiff.Stat.Insertions)
		diffStats.Deletions = int64(niceDiff.Stat.Deletions)
		filesChanged = niceDiff.Stat.FilesChanged
	}

	card, err := s.drawPullSummaryCard(pull, f, commentCount, diffStats, filesChanged)
	if err != nil {
		log.Println("failed to draw pull summary card", err)
		http.Error(w, "failed to draw pull summary card", http.StatusInternalServerError)
		return
	}

	var imageBuffer bytes.Buffer
	err = png.Encode(&imageBuffer, card.Img)
	if err != nil {
		log.Println("failed to encode pull summary card", err)
		http.Error(w, "failed to encode pull summary card", http.StatusInternalServerError)
		return
	}

	imageBytes := imageBuffer.Bytes()

	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "public, max-age=3600") // 1 hour
	w.WriteHeader(http.StatusOK)
	_, err = w.Write(imageBytes)
	if err != nil {
		log.Println("failed to write pull summary card", err)
		return
	}
}
