package validator

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/bluesky-social/indigo/atproto/syntax"
	"golang.org/x/exp/slices"
	"tangled.org/core/api/tangled"
	"tangled.org/core/appview/models"
)

var (
	// Label name should be alphanumeric with hyphens/underscores, but not start/end with them
	labelNameRegex = regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9_-]*[a-zA-Z0-9])?$`)
	// Color should be a valid hex color
	colorRegex = regexp.MustCompile(`^#[a-fA-F0-9]{6}$`)
	// You can only label issues and pulls presently
	validScopes = []string{tangled.RepoIssueNSID, tangled.RepoPullNSID}
)

func (v *Validator) ValidateLabelDefinition(label *models.LabelDefinition) error {
	if label.Name == "" {
		return fmt.Errorf("label name is empty")
	}
	if len(label.Name) > 40 {
		return fmt.Errorf("label name too long (max 40 graphemes)")
	}
	if len(label.Name) < 1 {
		return fmt.Errorf("label name too short (min 1 grapheme)")
	}
	if !labelNameRegex.MatchString(label.Name) {
		return fmt.Errorf("label name contains invalid characters (use only letters, numbers, hyphens, and underscores)")
	}

	if !label.ValueType.IsConcreteType() {
		return fmt.Errorf("invalid value type: %q (must be one of: null, boolean, integer, string)", label.ValueType.Type)
	}

	// null type checks: cannot be enums, multiple or explicit format
	if label.ValueType.IsNull() && label.ValueType.IsEnum() {
		return fmt.Errorf("null type cannot be used in conjunction with enum type")
	}
	if label.ValueType.IsNull() && label.Multiple {
		return fmt.Errorf("null type labels cannot be multiple")
	}
	if label.ValueType.IsNull() && !label.ValueType.IsAnyFormat() {
		return fmt.Errorf("format cannot be used in conjunction with null type")
	}

	// format checks: cannot be used with enum, or integers
	if !label.ValueType.IsAnyFormat() && label.ValueType.IsEnum() {
		return fmt.Errorf("enum types cannot be used in conjunction with format specification")
	}

	if !label.ValueType.IsAnyFormat() && !label.ValueType.IsString() {
		return fmt.Errorf("format specifications are only permitted on string types")
	}

	// validate scope (nsid format)
	if label.Scope == nil {
		return fmt.Errorf("scope is required")
	}
	for _, s := range label.Scope {
		if _, err := syntax.ParseNSID(s); err != nil {
			return fmt.Errorf("failed to parse scope: %w", err)
		}
		if !slices.Contains(validScopes, s) {
			return fmt.Errorf("invalid scope: scope must be present in %q", validScopes)
		}
	}

	// validate color if provided
	if label.Color != nil {
		color := strings.TrimSpace(*label.Color)
		if color == "" {
			// empty color is fine, set to nil
			label.Color = nil
		} else {
			if !colorRegex.MatchString(color) {
				return fmt.Errorf("color must be a valid hex color (e.g. #79FFE1 or #000)")
			}
			// expand 3-digit hex to 6-digit hex
			if len(color) == 4 { // #ABC
				color = fmt.Sprintf("#%c%c%c%c%c%c", color[1], color[1], color[2], color[2], color[3], color[3])
			}
			// convert to uppercase for consistency
			color = strings.ToUpper(color)
			label.Color = &color
		}
	}

	return nil
}

func (v *Validator) ValidateLabelOp(labelDef *models.LabelDefinition, repo *models.Repo, labelOp *models.LabelOp) error {
	if labelDef == nil {
		return fmt.Errorf("label definition is required")
	}
	if repo == nil {
		return fmt.Errorf("repo is required")
	}
	if labelOp == nil {
		return fmt.Errorf("label operation is required")
	}

	// validate permissions: only collaborators can apply labels currently
	//
	// TODO: introduce a repo:triage permission
	ok, err := v.enforcer.IsPushAllowed(labelOp.Did, repo.Knot, repo.DidSlashRepo())
	if err != nil {
		return fmt.Errorf("failed to enforce permissions: %w", err)
	}
	if !ok {
		return fmt.Errorf("unauhtorized label operation")
	}

	expectedKey := labelDef.AtUri().String()
	if labelOp.OperandKey != expectedKey {
		return fmt.Errorf("operand key %q does not match label definition URI %q", labelOp.OperandKey, expectedKey)
	}

	if labelOp.Operation != models.LabelOperationAdd && labelOp.Operation != models.LabelOperationDel {
		return fmt.Errorf("invalid operation: %q (must be 'add' or 'del')", labelOp.Operation)
	}

	if labelOp.Subject == "" {
		return fmt.Errorf("subject URI is required")
	}
	if _, err := syntax.ParseATURI(string(labelOp.Subject)); err != nil {
		return fmt.Errorf("invalid subject URI: %w", err)
	}

	if err := v.validateOperandValue(labelDef, labelOp); err != nil {
		return fmt.Errorf("invalid operand value: %w", err)
	}

	// Validate performed time is not zero/invalid
	if labelOp.PerformedAt.IsZero() {
		return fmt.Errorf("performed_at timestamp is required")
	}

	return nil
}

func (v *Validator) validateOperandValue(labelDef *models.LabelDefinition, labelOp *models.LabelOp) error {
	valueType := labelDef.ValueType

	// this is permitted, it "unsets" a label
	if labelOp.OperandValue == "" {
		labelOp.Operation = models.LabelOperationDel
		return nil
	}

	switch valueType.Type {
	case models.ConcreteTypeNull:
		// For null type, value should be empty
		if labelOp.OperandValue != "null" {
			return fmt.Errorf("null type requires empty value, got %q", labelOp.OperandValue)
		}

	case models.ConcreteTypeString:
		// For string type, validate enum constraints if present
		if valueType.IsEnum() {
			if !slices.Contains(valueType.Enum, labelOp.OperandValue) {
				return fmt.Errorf("value %q is not in allowed enum values %v", labelOp.OperandValue, valueType.Enum)
			}
		}

		switch valueType.Format {
		case models.ValueTypeFormatDid:
			id, err := v.resolver.ResolveIdent(context.Background(), labelOp.OperandValue)
			if err != nil {
				return fmt.Errorf("failed to resolve did/handle: %w", err)
			}

			labelOp.OperandValue = id.DID.String()

		case models.ValueTypeFormatAny, "":
		default:
			return fmt.Errorf("unsupported format constraint: %q", valueType.Format)
		}

	case models.ConcreteTypeInt:
		if labelOp.OperandValue == "" {
			return fmt.Errorf("integer type requires non-empty value")
		}
		if _, err := fmt.Sscanf(labelOp.OperandValue, "%d", new(int)); err != nil {
			return fmt.Errorf("value %q is not a valid integer", labelOp.OperandValue)
		}

		if valueType.IsEnum() {
			if !slices.Contains(valueType.Enum, labelOp.OperandValue) {
				return fmt.Errorf("value %q is not in allowed enum values %v", labelOp.OperandValue, valueType.Enum)
			}
		}

	case models.ConcreteTypeBool:
		if labelOp.OperandValue != "true" && labelOp.OperandValue != "false" {
			return fmt.Errorf("boolean type requires value to be 'true' or 'false', got %q", labelOp.OperandValue)
		}

		// validate enum constraints if present (though uncommon for booleans)
		if valueType.IsEnum() {
			if !slices.Contains(valueType.Enum, labelOp.OperandValue) {
				return fmt.Errorf("value %q is not in allowed enum values %v", labelOp.OperandValue, valueType.Enum)
			}
		}

	default:
		return fmt.Errorf("unsupported value type: %q", valueType.Type)
	}

	return nil
}
