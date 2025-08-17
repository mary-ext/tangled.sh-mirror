package db

import (
	"fmt"
	"log"
	"strings"
	"time"
)

type Follow struct {
	UserDid    string
	SubjectDid string
	FollowedAt time.Time
	Rkey       string
}

func AddFollow(e Execer, follow *Follow) error {
	query := `insert or ignore into follows (user_did, subject_did, rkey) values (?, ?, ?)`
	_, err := e.Exec(query, follow.UserDid, follow.SubjectDid, follow.Rkey)
	return err
}

// Get a follow record
func GetFollow(e Execer, userDid, subjectDid string) (*Follow, error) {
	query := `select user_did, subject_did, followed_at, rkey from follows where user_did = ? and subject_did = ?`
	row := e.QueryRow(query, userDid, subjectDid)

	var follow Follow
	var followedAt string
	err := row.Scan(&follow.UserDid, &follow.SubjectDid, &followedAt, &follow.Rkey)
	if err != nil {
		return nil, err
	}

	followedAtTime, err := time.Parse(time.RFC3339, followedAt)
	if err != nil {
		log.Println("unable to determine followed at time")
		follow.FollowedAt = time.Now()
	} else {
		follow.FollowedAt = followedAtTime
	}

	return &follow, nil
}

// Remove a follow
func DeleteFollow(e Execer, userDid, subjectDid string) error {
	_, err := e.Exec(`delete from follows where user_did = ? and subject_did = ?`, userDid, subjectDid)
	return err
}

// Remove a follow
func DeleteFollowByRkey(e Execer, userDid, rkey string) error {
	_, err := e.Exec(`delete from follows where user_did = ? and rkey = ?`, userDid, rkey)
	return err
}

type FollowStats struct {
	Followers int
	Following int
}

func GetFollowerFollowingCount(e Execer, did string) (FollowStats, error) {
	followers, following := 0, 0
	err := e.QueryRow(
		`SELECT
		COUNT(CASE WHEN subject_did = ? THEN 1 END) AS followers,
		COUNT(CASE WHEN user_did = ? THEN 1 END) AS following
		FROM follows;`, did, did).Scan(&followers, &following)
	if err != nil {
		return FollowStats{}, err
	}
	return FollowStats{
		Followers: followers,
		Following: following,
	}, nil
}

func GetFollowerFollowingCounts(e Execer, dids []string) (map[string]FollowStats, error) {
	if len(dids) == 0 {
		return nil, nil
	}

	placeholders := make([]string, len(dids))
	for i := range placeholders {
		placeholders[i] = "?"
	}
	placeholderStr := strings.Join(placeholders, ",")

	args := make([]any, len(dids)*2)
	for i, did := range dids {
		args[i] = did
		args[i+len(dids)] = did
	}

	query := fmt.Sprintf(`
		select
			coalesce(f.did, g.did) as did,
			coalesce(f.followers, 0) as followers,
			coalesce(g.following, 0) as following
		from (
			select subject_did as did, count(*) as followers
			from follows
			where subject_did in (%s)
			group by subject_did
		) f
		full outer join (
			select user_did as did, count(*) as following
			from follows
			where user_did in (%s)
			group by user_did
		) g on f.did = g.did`,
		placeholderStr, placeholderStr)

	result := make(map[string]FollowStats)

	rows, err := e.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var did string
		var followers, following int
		if err := rows.Scan(&did, &followers, &following); err != nil {
			return nil, err
		}
		result[did] = FollowStats{
			Followers: followers,
			Following: following,
		}
	}

	for _, did := range dids {
		if _, exists := result[did]; !exists {
			result[did] = FollowStats{
				Followers: 0,
				Following: 0,
			}
		}
	}

	return result, nil
}

func GetFollows(e Execer, limit int, filters ...filter) ([]Follow, error) {
	var follows []Follow

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
	limitClause := ""
	if limit > 0 {
		limitClause = " limit ?"
		args = append(args, limit)
	}

	query := fmt.Sprintf(
		`select user_did, subject_did, followed_at, rkey
		from follows
		%s
		order by followed_at desc
		%s
	`, whereClause, limitClause)

	rows, err := e.Query(query, args...)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var follow Follow
		var followedAt string
		err := rows.Scan(
			&follow.UserDid,
			&follow.SubjectDid,
			&followedAt,
			&follow.Rkey,
		)
		if err != nil {
			return nil, err
		}
		followedAtTime, err := time.Parse(time.RFC3339, followedAt)
		if err != nil {
			log.Println("unable to determine followed at time")
			follow.FollowedAt = time.Now()
		} else {
			follow.FollowedAt = followedAtTime
		}
		follows = append(follows, follow)
	}
	return follows, nil
}

func GetFollowers(e Execer, did string) ([]Follow, error) {
	return GetFollows(e, 0, FilterEq("subject_did", did))
}

func GetFollowing(e Execer, did string) ([]Follow, error) {
	return GetFollows(e, 0, FilterEq("user_did", did))
}

type FollowStatus int

const (
	IsNotFollowing FollowStatus = iota
	IsFollowing
	IsSelf
)

func (s FollowStatus) String() string {
	switch s {
	case IsNotFollowing:
		return "IsNotFollowing"
	case IsFollowing:
		return "IsFollowing"
	case IsSelf:
		return "IsSelf"
	default:
		return "IsNotFollowing"
	}
}

func GetFollowStatus(e Execer, userDid, subjectDid string) FollowStatus {
	if userDid == subjectDid {
		return IsSelf
	} else if _, err := GetFollow(e, userDid, subjectDid); err != nil {
		return IsNotFollowing
	} else {
		return IsFollowing
	}
}
