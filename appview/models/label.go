package models

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/bluesky-social/indigo/api/atproto"
	"github.com/bluesky-social/indigo/atproto/syntax"
	"github.com/bluesky-social/indigo/xrpc"
	"tangled.org/core/api/tangled"
	"tangled.org/core/consts"
	"tangled.org/core/idresolver"
)

type ConcreteType string

const (
	ConcreteTypeNull   ConcreteType = "null"
	ConcreteTypeString ConcreteType = "string"
	ConcreteTypeInt    ConcreteType = "integer"
	ConcreteTypeBool   ConcreteType = "boolean"
)

type ValueTypeFormat string

const (
	ValueTypeFormatAny ValueTypeFormat = "any"
	ValueTypeFormatDid ValueTypeFormat = "did"
)

// ValueType represents an atproto lexicon type definition with constraints
type ValueType struct {
	Type   ConcreteType    `json:"type"`
	Format ValueTypeFormat `json:"format,omitempty"`
	Enum   []string        `json:"enum,omitempty"`
}

func (vt *ValueType) AsRecord() tangled.LabelDefinition_ValueType {
	return tangled.LabelDefinition_ValueType{
		Type:   string(vt.Type),
		Format: string(vt.Format),
		Enum:   vt.Enum,
	}
}

func ValueTypeFromRecord(record tangled.LabelDefinition_ValueType) ValueType {
	return ValueType{
		Type:   ConcreteType(record.Type),
		Format: ValueTypeFormat(record.Format),
		Enum:   record.Enum,
	}
}

func (vt ValueType) IsConcreteType() bool {
	return vt.Type == ConcreteTypeNull ||
		vt.Type == ConcreteTypeString ||
		vt.Type == ConcreteTypeInt ||
		vt.Type == ConcreteTypeBool
}

func (vt ValueType) IsNull() bool {
	return vt.Type == ConcreteTypeNull
}

func (vt ValueType) IsString() bool {
	return vt.Type == ConcreteTypeString
}

func (vt ValueType) IsInt() bool {
	return vt.Type == ConcreteTypeInt
}

func (vt ValueType) IsBool() bool {
	return vt.Type == ConcreteTypeBool
}

func (vt ValueType) IsEnum() bool {
	return len(vt.Enum) > 0
}

func (vt ValueType) IsDidFormat() bool {
	return vt.Format == ValueTypeFormatDid
}

func (vt ValueType) IsAnyFormat() bool {
	return vt.Format == ValueTypeFormatAny
}

type LabelDefinition struct {
	Id   int64
	Did  string
	Rkey string

	Name      string
	ValueType ValueType
	Scope     []string
	Color     *string
	Multiple  bool
	Created   time.Time
}

func (l *LabelDefinition) AtUri() syntax.ATURI {
	return syntax.ATURI(fmt.Sprintf("at://%s/%s/%s", l.Did, tangled.LabelDefinitionNSID, l.Rkey))
}

func (l *LabelDefinition) AsRecord() tangled.LabelDefinition {
	vt := l.ValueType.AsRecord()
	return tangled.LabelDefinition{
		Name:      l.Name,
		Color:     l.Color,
		CreatedAt: l.Created.Format(time.RFC3339),
		Multiple:  &l.Multiple,
		Scope:     l.Scope,
		ValueType: &vt,
	}
}

// random color for a given seed
func randomColor(seed string) string {
	hash := sha1.Sum([]byte(seed))
	hexStr := hex.EncodeToString(hash[:])
	r := hexStr[0:2]
	g := hexStr[2:4]
	b := hexStr[4:6]

	return fmt.Sprintf("#%s%s%s", r, g, b)
}

func (ld LabelDefinition) GetColor() string {
	if ld.Color == nil {
		seed := fmt.Sprintf("%d:%s:%s", ld.Id, ld.Did, ld.Rkey)
		color := randomColor(seed)
		return color
	}

	return *ld.Color
}

func LabelDefinitionFromRecord(did, rkey string, record tangled.LabelDefinition) (*LabelDefinition, error) {
	created, err := time.Parse(time.RFC3339, record.CreatedAt)
	if err != nil {
		created = time.Now()
	}

	multiple := false
	if record.Multiple != nil {
		multiple = *record.Multiple
	}

	var vt ValueType
	if record.ValueType != nil {
		vt = ValueTypeFromRecord(*record.ValueType)
	}

	return &LabelDefinition{
		Did:  did,
		Rkey: rkey,

		Name:      record.Name,
		ValueType: vt,
		Scope:     record.Scope,
		Color:     record.Color,
		Multiple:  multiple,
		Created:   created,
	}, nil
}

type LabelOp struct {
	Id           int64
	Did          string
	Rkey         string
	Subject      syntax.ATURI
	Operation    LabelOperation
	OperandKey   string
	OperandValue string
	PerformedAt  time.Time
	IndexedAt    time.Time
}

func (l LabelOp) SortAt() time.Time {
	createdAt := l.PerformedAt
	indexedAt := l.IndexedAt

	// if we don't have an indexedat, fall back to now
	if indexedAt.IsZero() {
		indexedAt = time.Now()
	}

	// if createdat is invalid (before epoch), treat as null -> return zero time
	if createdAt.Before(time.UnixMicro(0)) {
		return time.Time{}
	}

	// if createdat is <= indexedat, use createdat
	if createdAt.Before(indexedAt) || createdAt.Equal(indexedAt) {
		return createdAt
	}

	// otherwise, createdat is in the future relative to indexedat -> use indexedat
	return indexedAt
}

type LabelOperation string

const (
	LabelOperationAdd LabelOperation = "add"
	LabelOperationDel LabelOperation = "del"
)

// a record can create multiple label ops
func LabelOpsFromRecord(did, rkey string, record tangled.LabelOp) []LabelOp {
	performed, err := time.Parse(time.RFC3339, record.PerformedAt)
	if err != nil {
		performed = time.Now()
	}

	mkOp := func(operand *tangled.LabelOp_Operand) LabelOp {
		return LabelOp{
			Did:          did,
			Rkey:         rkey,
			Subject:      syntax.ATURI(record.Subject),
			OperandKey:   operand.Key,
			OperandValue: operand.Value,
			PerformedAt:  performed,
		}
	}

	var ops []LabelOp
	// deletes first, then additions
	for _, o := range record.Delete {
		if o != nil {
			op := mkOp(o)
			op.Operation = LabelOperationDel
			ops = append(ops, op)
		}
	}
	for _, o := range record.Add {
		if o != nil {
			op := mkOp(o)
			op.Operation = LabelOperationAdd
			ops = append(ops, op)
		}
	}

	return ops
}

func LabelOpsAsRecord(ops []LabelOp) tangled.LabelOp {
	if len(ops) == 0 {
		return tangled.LabelOp{}
	}

	// use the first operation to establish common fields
	first := ops[0]
	record := tangled.LabelOp{
		Subject:     string(first.Subject),
		PerformedAt: first.PerformedAt.Format(time.RFC3339),
	}

	var addOperands []*tangled.LabelOp_Operand
	var deleteOperands []*tangled.LabelOp_Operand

	for _, op := range ops {
		operand := &tangled.LabelOp_Operand{
			Key:   op.OperandKey,
			Value: op.OperandValue,
		}

		switch op.Operation {
		case LabelOperationAdd:
			addOperands = append(addOperands, operand)
		case LabelOperationDel:
			deleteOperands = append(deleteOperands, operand)
		default:
			return tangled.LabelOp{}
		}
	}

	record.Add = addOperands
	record.Delete = deleteOperands

	return record
}

type set = map[string]struct{}

type LabelState struct {
	inner map[string]set
}

func NewLabelState() LabelState {
	return LabelState{
		inner: make(map[string]set),
	}
}

func (s LabelState) Inner() map[string]set {
	return s.inner
}

func (s LabelState) ContainsLabel(l string) bool {
	if valset, exists := s.inner[l]; exists {
		if valset != nil {
			return true
		}
	}

	return false
}

// go maps behavior in templates make this necessary,
// indexing a map and getting `set` in return is apparently truthy
func (s LabelState) ContainsLabelAndVal(l, v string) bool {
	if valset, exists := s.inner[l]; exists {
		if _, exists := valset[v]; exists {
			return true
		}
	}

	return false
}

func (s LabelState) GetValSet(l string) set {
	if valset, exists := s.inner[l]; exists {
		return valset
	} else {
		return make(set)
	}
}

type LabelApplicationCtx struct {
	Defs map[string]*LabelDefinition // labelAt -> labelDef
}

var (
	LabelNoOpError = errors.New("no-op")
)

func (c *LabelApplicationCtx) ApplyLabelOp(state LabelState, op LabelOp) error {
	def, ok := c.Defs[op.OperandKey]
	if !ok {
		// this def was deleted, but an op exists, so we just skip over the op
		return nil
	}

	switch op.Operation {
	case LabelOperationAdd:
		// if valueset is empty, init it
		if state.inner[op.OperandKey] == nil {
			state.inner[op.OperandKey] = make(set)
		}

		// if valueset is populated & this val alr exists, this labelop is a noop
		if valueSet, exists := state.inner[op.OperandKey]; exists {
			if _, exists = valueSet[op.OperandValue]; exists {
				return LabelNoOpError
			}
		}

		if def.Multiple {
			// append to set
			state.inner[op.OperandKey][op.OperandValue] = struct{}{}
		} else {
			// reset to just this value
			state.inner[op.OperandKey] = set{op.OperandValue: struct{}{}}
		}

	case LabelOperationDel:
		// if label DNE, then deletion is a no-op
		if valueSet, exists := state.inner[op.OperandKey]; !exists {
			return LabelNoOpError
		} else if _, exists = valueSet[op.OperandValue]; !exists { // if value DNE, then deletion is no-op
			return LabelNoOpError
		}

		if def.Multiple {
			// remove from set
			delete(state.inner[op.OperandKey], op.OperandValue)
		} else {
			// reset the entire label
			delete(state.inner, op.OperandKey)
		}

		// if the map becomes empty, then set it to nil, this is just the inverse of add
		if len(state.inner[op.OperandKey]) == 0 {
			state.inner[op.OperandKey] = nil
		}

	}

	return nil
}

func (c *LabelApplicationCtx) ApplyLabelOps(state LabelState, ops []LabelOp) {
	// sort label ops in sort order first
	slices.SortFunc(ops, func(a, b LabelOp) int {
		return a.SortAt().Compare(b.SortAt())
	})

	// apply ops in sequence
	for _, o := range ops {
		_ = c.ApplyLabelOp(state, o)
	}
}

// IsInverse checks if one label operation is the inverse of another
// returns true if one is an add and the other is a delete with the same key and value
func (op1 LabelOp) IsInverse(op2 LabelOp) bool {
	if op1.OperandKey != op2.OperandKey || op1.OperandValue != op2.OperandValue {
		return false
	}

	return (op1.Operation == LabelOperationAdd && op2.Operation == LabelOperationDel) ||
		(op1.Operation == LabelOperationDel && op2.Operation == LabelOperationAdd)
}

// removes pairs of label operations that are inverses of each other
// from the given slice. the function preserves the order of remaining operations.
func ReduceLabelOps(ops []LabelOp) []LabelOp {
	if len(ops) <= 1 {
		return ops
	}

	keep := make([]bool, len(ops))
	for i := range keep {
		keep[i] = true
	}

	for i := range ops {
		if !keep[i] {
			continue
		}

		for j := i + 1; j < len(ops); j++ {
			if !keep[j] {
				continue
			}

			if ops[i].IsInverse(ops[j]) {
				keep[i] = false
				keep[j] = false
				break // move to next i since this one is now eliminated
			}
		}
	}

	// build result slice with only kept operations
	var result []LabelOp
	for i, op := range ops {
		if keep[i] {
			result = append(result, op)
		}
	}

	return result
}

var (
	LabelWontfix        = fmt.Sprintf("at://%s/%s/%s", consts.TangledDid, tangled.LabelDefinitionNSID, "wontfix")
	LabelDuplicate      = fmt.Sprintf("at://%s/%s/%s", consts.TangledDid, tangled.LabelDefinitionNSID, "duplicate")
	LabelAssignee       = fmt.Sprintf("at://%s/%s/%s", consts.TangledDid, tangled.LabelDefinitionNSID, "assignee")
	LabelGoodFirstIssue = fmt.Sprintf("at://%s/%s/%s", consts.TangledDid, tangled.LabelDefinitionNSID, "good-first-issue")
	LabelDocumentation  = fmt.Sprintf("at://%s/%s/%s", consts.TangledDid, tangled.LabelDefinitionNSID, "documentation")
)

func DefaultLabelDefs() []string {
	return []string{
		LabelWontfix,
		LabelDuplicate,
		LabelAssignee,
		LabelGoodFirstIssue,
		LabelDocumentation,
	}
}

func FetchDefaultDefs(r *idresolver.Resolver) ([]LabelDefinition, error) {
	resolved, err := r.ResolveIdent(context.Background(), consts.TangledDid)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve tangled.sh DID %s: %v", consts.TangledDid, err)
	}
	pdsEndpoint := resolved.PDSEndpoint()
	if pdsEndpoint == "" {
		return nil, fmt.Errorf("no PDS endpoint found for tangled.sh DID %s", consts.TangledDid)
	}
	client := &xrpc.Client{
		Host: pdsEndpoint,
	}

	var labelDefs []LabelDefinition

	for _, dl := range DefaultLabelDefs() {
		atUri := syntax.ATURI(dl)
		parsedUri, err := syntax.ParseATURI(string(atUri))
		if err != nil {
			return nil, fmt.Errorf("failed to parse AT-URI %s: %v", atUri, err)
		}
		record, err := atproto.RepoGetRecord(
			context.Background(),
			client,
			"",
			parsedUri.Collection().String(),
			parsedUri.Authority().String(),
			parsedUri.RecordKey().String(),
		)
		if err != nil {
			return nil, fmt.Errorf("failed to get record for %s: %v", atUri, err)
		}

		if record != nil {
			bytes, err := record.Value.MarshalJSON()
			if err != nil {
				return nil, fmt.Errorf("failed to marshal record value for %s: %v", atUri, err)
			}

			raw := json.RawMessage(bytes)
			labelRecord := tangled.LabelDefinition{}
			err = json.Unmarshal(raw, &labelRecord)
			if err != nil {
				return nil, fmt.Errorf("invalid record for %s: %w", atUri, err)
			}

			labelDef, err := LabelDefinitionFromRecord(
				parsedUri.Authority().String(),
				parsedUri.RecordKey().String(),
				labelRecord,
			)
			if err != nil {
				return nil, fmt.Errorf("failed to create label definition from record %s: %v", atUri, err)
			}

			labelDefs = append(labelDefs, *labelDef)
		}
	}

	return labelDefs, nil
}
