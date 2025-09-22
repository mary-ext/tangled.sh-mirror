package db

import (
	"database/sql"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/bluesky-social/indigo/atproto/syntax"
	"tangled.org/core/appview/models"
)

// no updating type for now
func AddLabelDefinition(e Execer, l *models.LabelDefinition) (int64, error) {
	result, err := e.Exec(
		`insert into label_definitions (
			did,
			rkey,
			name,
			value_type,
			value_format,
			value_enum,
			scope,
			color,
			multiple,
			created
		)
		values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		on conflict(did, rkey) do update set
			name = excluded.name,
			scope = excluded.scope,
			color = excluded.color,
			multiple = excluded.multiple`,
		l.Did,
		l.Rkey,
		l.Name,
		l.ValueType.Type,
		l.ValueType.Format,
		strings.Join(l.ValueType.Enum, ","),
		strings.Join(l.Scope, ","),
		l.Color,
		l.Multiple,
		l.Created.Format(time.RFC3339),
		time.Now().Format(time.RFC3339),
	)
	if err != nil {
		return 0, err
	}

	id, err := result.LastInsertId()
	if err != nil {
		return 0, err
	}

	l.Id = id

	return id, nil
}

func DeleteLabelDefinition(e Execer, filters ...filter) error {
	var conditions []string
	var args []any
	for _, filter := range filters {
		conditions = append(conditions, filter.Condition())
		args = append(args, filter.Arg()...)
	}
	whereClause := ""
	if conditions != nil {
		whereClause = " where " + strings.Join(conditions, " and ")
	}
	query := fmt.Sprintf(`delete from label_definitions %s`, whereClause)
	_, err := e.Exec(query, args...)
	return err
}

func GetLabelDefinitions(e Execer, filters ...filter) ([]models.LabelDefinition, error) {
	var labelDefinitions []models.LabelDefinition
	var conditions []string
	var args []any

	for _, filter := range filters {
		conditions = append(conditions, filter.Condition())
		args = append(args, filter.Arg()...)
	}

	whereClause := ""
	if conditions != nil {
		whereClause = " where " + strings.Join(conditions, " and ")
	}

	query := fmt.Sprintf(
		`
		select 
			id,
			did,
			rkey,
			name,
			value_type,
			value_format,
			value_enum,
			scope,
			color,
			multiple,
			created
		from label_definitions
		%s
		order by created
		`,
		whereClause,
	)

	rows, err := e.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var labelDefinition models.LabelDefinition
		var createdAt, enumVariants, scopes string
		var color sql.Null[string]
		var multiple int

		if err := rows.Scan(
			&labelDefinition.Id,
			&labelDefinition.Did,
			&labelDefinition.Rkey,
			&labelDefinition.Name,
			&labelDefinition.ValueType.Type,
			&labelDefinition.ValueType.Format,
			&enumVariants,
			&scopes,
			&color,
			&multiple,
			&createdAt,
		); err != nil {
			return nil, err
		}

		labelDefinition.Created, err = time.Parse(time.RFC3339, createdAt)
		if err != nil {
			labelDefinition.Created = time.Now()
		}

		if color.Valid {
			labelDefinition.Color = &color.V
		}

		if multiple != 0 {
			labelDefinition.Multiple = true
		}

		if enumVariants != "" {
			labelDefinition.ValueType.Enum = strings.Split(enumVariants, ",")
		}

		for s := range strings.SplitSeq(scopes, ",") {
			labelDefinition.Scope = append(labelDefinition.Scope, s)
		}

		labelDefinitions = append(labelDefinitions, labelDefinition)
	}

	return labelDefinitions, nil
}

// helper to get exactly one label def
func GetLabelDefinition(e Execer, filters ...filter) (*models.LabelDefinition, error) {
	labels, err := GetLabelDefinitions(e, filters...)
	if err != nil {
		return nil, err
	}

	if labels == nil {
		return nil, sql.ErrNoRows
	}

	if len(labels) != 1 {
		return nil, fmt.Errorf("too many rows returned")
	}

	return &labels[0], nil
}

func AddLabelOp(e Execer, l *models.LabelOp) (int64, error) {
	now := time.Now()
	result, err := e.Exec(
		`insert into label_ops (
			did,
			rkey,
			subject,
			operation,
			operand_key,
			operand_value,
			performed,
			indexed
		)
		values (?, ?, ?, ?, ?, ?, ?, ?)
		on conflict(did, rkey, subject, operand_key, operand_value) do update set
			operation = excluded.operation,
			operand_value = excluded.operand_value,
			performed = excluded.performed,
			indexed = excluded.indexed`,
		l.Did,
		l.Rkey,
		l.Subject.String(),
		string(l.Operation),
		l.OperandKey,
		l.OperandValue,
		l.PerformedAt.Format(time.RFC3339),
		now.Format(time.RFC3339),
	)
	if err != nil {
		return 0, err
	}

	id, err := result.LastInsertId()
	if err != nil {
		return 0, err
	}

	l.Id = id
	l.IndexedAt = now

	return id, nil
}

func GetLabelOps(e Execer, filters ...filter) ([]models.LabelOp, error) {
	var labelOps []models.LabelOp
	var conditions []string
	var args []any

	for _, filter := range filters {
		conditions = append(conditions, filter.Condition())
		args = append(args, filter.Arg()...)
	}

	whereClause := ""
	if conditions != nil {
		whereClause = " where " + strings.Join(conditions, " and ")
	}

	query := fmt.Sprintf(
		`
		select
			id,
			did,
			rkey,
			subject,
			operation,
			operand_key,
			operand_value,
			performed,
			indexed
		from label_ops
		%s
		order by indexed
		`,
		whereClause,
	)

	rows, err := e.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var labelOp models.LabelOp
		var performedAt, indexedAt string

		if err := rows.Scan(
			&labelOp.Id,
			&labelOp.Did,
			&labelOp.Rkey,
			&labelOp.Subject,
			&labelOp.Operation,
			&labelOp.OperandKey,
			&labelOp.OperandValue,
			&performedAt,
			&indexedAt,
		); err != nil {
			return nil, err
		}

		labelOp.PerformedAt, err = time.Parse(time.RFC3339, performedAt)
		if err != nil {
			labelOp.PerformedAt = time.Now()
		}

		labelOp.IndexedAt, err = time.Parse(time.RFC3339, indexedAt)
		if err != nil {
			labelOp.IndexedAt = time.Now()
		}

		labelOps = append(labelOps, labelOp)
	}

	return labelOps, nil
}

// get labels for a given list of subject URIs
func GetLabels(e Execer, filters ...filter) (map[syntax.ATURI]models.LabelState, error) {
	ops, err := GetLabelOps(e, filters...)
	if err != nil {
		return nil, err
	}

	// group ops by subject
	opsBySubject := make(map[syntax.ATURI][]models.LabelOp)
	for _, op := range ops {
		subject := syntax.ATURI(op.Subject)
		opsBySubject[subject] = append(opsBySubject[subject], op)
	}

	// get all unique labelats for creating the context
	labelAtSet := make(map[string]bool)
	for _, op := range ops {
		labelAtSet[op.OperandKey] = true
	}
	labelAts := slices.Collect(maps.Keys(labelAtSet))

	actx, err := NewLabelApplicationCtx(e, FilterIn("at_uri", labelAts))
	if err != nil {
		return nil, err
	}

	// apply label ops for each subject and collect results
	results := make(map[syntax.ATURI]models.LabelState)
	for subject, subjectOps := range opsBySubject {
		state := models.NewLabelState()
		actx.ApplyLabelOps(state, subjectOps)
		results[subject] = state
	}

	return results, nil
}

func NewLabelApplicationCtx(e Execer, filters ...filter) (*models.LabelApplicationCtx, error) {
	labels, err := GetLabelDefinitions(e, filters...)
	if err != nil {
		return nil, err
	}

	defs := make(map[string]*models.LabelDefinition)
	for _, l := range labels {
		defs[l.AtUri().String()] = &l
	}

	return &models.LabelApplicationCtx{defs}, nil
}
