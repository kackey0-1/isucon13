package main

import (
	"database/sql"
	"errors"
	"net/http"
	"sort"
	"strconv"

	"github.com/jmoiron/sqlx"
	"github.com/labstack/echo/v4"
)

type LivestreamStatistics struct {
	Rank           int64 `json:"rank"`
	ViewersCount   int64 `json:"viewers_count"`
	TotalReactions int64 `json:"total_reactions"`
	TotalReports   int64 `json:"total_reports"`
	MaxTip         int64 `json:"max_tip"`
}

type LivestreamRankingEntry struct {
	LivestreamID int64
	Score        int64
}
type LivestreamRanking []LivestreamRankingEntry

func (r LivestreamRanking) Len() int      { return len(r) }
func (r LivestreamRanking) Swap(i, j int) { r[i], r[j] = r[j], r[i] }
func (r LivestreamRanking) Less(i, j int) bool {
	if r[i].Score == r[j].Score {
		return r[i].LivestreamID < r[j].LivestreamID
	} else {
		return r[i].Score < r[j].Score
	}
}

type UserStatistics struct {
	Rank              int64  `json:"rank"`
	ViewersCount      int64  `json:"viewers_count"`
	TotalReactions    int64  `json:"total_reactions"`
	TotalLivecomments int64  `json:"total_livecomments"`
	TotalTip          int64  `json:"total_tip"`
	FavoriteEmoji     string `json:"favorite_emoji"`
}

type UserRankingEntry struct {
	Username string
	Score    int64
}
type UserRanking []UserRankingEntry

func (r UserRanking) Len() int      { return len(r) }
func (r UserRanking) Swap(i, j int) { r[i], r[j] = r[j], r[i] }
func (r UserRanking) Less(i, j int) bool {
	if r[i].Score == r[j].Score {
		return r[i].Username < r[j].Username
	} else {
		return r[i].Score < r[j].Score
	}
}

func getUserStatisticsHandler(c echo.Context) error {
	ctx := c.Request().Context()

	if err := verifyUserSession(c); err != nil {
		return err
	}

	username := c.Param("username")

	tx, err := dbConn.BeginTxx(ctx, nil)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to begin transaction: "+err.Error())
	}
	defer tx.Rollback()

	var user UserModel
	if err := tx.GetContext(ctx, &user, "SELECT * FROM users WHERE name = ?", username); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return echo.NewHTTPError(http.StatusBadRequest, "not found user that has the given username")
		} else {
			return echo.NewHTTPError(http.StatusInternalServerError, "failed to get user: "+err.Error())
		}
	}

	// 全ユーザーを取得
	var users []*UserModel
	if err := tx.SelectContext(ctx, &users, "SELECT * FROM users"); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to get users: "+err.Error())
	}

	// ユーザーIDのスライスを作成
	var userIDs []int64
	userIDToName := make(map[int64]string)
	for _, u := range users {
		userIDs = append(userIDs, u.ID)
		userIDToName[u.ID] = u.Name
	}

	// リアクション数とチップ額を一度に取得
	query, args, err := sqlx.In(`
		SELECT 
			u.id AS user_id,
			COUNT(r.id) AS reactions,
			IFNULL(SUM(lc.tip), 0) AS tips
		FROM users u
		LEFT JOIN livestreams l ON l.user_id = u.id
		LEFT JOIN reactions r ON r.livestream_id = l.id
		LEFT JOIN livecomments lc ON lc.livestream_id = l.id
		WHERE u.id IN (?)
		GROUP BY u.id
	`, userIDs)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to build query: "+err.Error())
	}

	// プレースホルダをリバインド
	query = tx.Rebind(query)

	// クエリの実行
	type UserScore struct {
		UserID    int64 `db:"user_id"`
		Reactions int64 `db:"reactions"`
		Tips      int64 `db:"tips"`
	}
	var userScores []UserScore
	if err := tx.SelectContext(ctx, &userScores, query, args...); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to get user scores: "+err.Error())
	}

	// スコアの計算とランキングの作成
	var ranking UserRanking
	userIDToScore := make(map[int64]int64)
	for _, us := range userScores {
		score := us.Reactions + us.Tips
		userIDToScore[us.UserID] = score
		ranking = append(ranking, UserRankingEntry{
			Username: userIDToName[us.UserID],
			Score:    score,
		})
	}

	// スコアでソート
	sort.Sort(sort.Reverse(ranking))

	// ユーザーのランクを取得
	var rank int64 = 1
	for i, entry := range ranking {
		if entry.Username == username {
			rank = int64(i + 1)
			break
		}
	}

	// リアクション数、ライブコメント数、チップ合計を取得
	var totalReactions int64
	var totalLivecomments int64
	var totalTip int64
	if err := tx.GetContext(ctx, &totalReactions, `
		SELECT COUNT(*) FROM reactions r
		INNER JOIN livestreams l ON l.id = r.livestream_id
		WHERE l.user_id = ?
	`, user.ID); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to count total reactions: "+err.Error())
	}

	if err := tx.GetContext(ctx, &totalLivecomments, `
		SELECT COUNT(*) FROM livecomments lc
		INNER JOIN livestreams l ON l.id = lc.livestream_id
		WHERE l.user_id = ?
	`, user.ID); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to count total livecomments: "+err.Error())
	}

	if err := tx.GetContext(ctx, &totalTip, `
		SELECT IFNULL(SUM(lc.tip), 0) FROM livecomments lc
		INNER JOIN livestreams l ON l.id = lc.livestream_id
		WHERE l.user_id = ?
	`, user.ID); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to sum total tips: "+err.Error())
	}

	// 合計視聴者数を取得
	var viewersCount int64
	if err := tx.GetContext(ctx, &viewersCount, `
		SELECT COUNT(DISTINCT vh.user_id) FROM livestream_viewers_history vh
		INNER JOIN livestreams l ON l.id = vh.livestream_id
		WHERE l.user_id = ?
	`, user.ID); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to count viewers: "+err.Error())
	}

	// お気に入り絵文字を取得
	var favoriteEmoji string
	if err := tx.GetContext(ctx, &favoriteEmoji, `
		SELECT r.emoji_name
		FROM reactions r
		INNER JOIN livestreams l ON l.id = r.livestream_id
		WHERE l.user_id = ?
		GROUP BY r.emoji_name
		ORDER BY COUNT(*) DESC, r.emoji_name DESC
		LIMIT 1
	`, user.ID); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to find favorite emoji: "+err.Error())
	}

	stats := UserStatistics{
		Rank:              rank,
		ViewersCount:      viewersCount,
		TotalReactions:    totalReactions,
		TotalLivecomments: totalLivecomments,
		TotalTip:          totalTip,
		FavoriteEmoji:     favoriteEmoji,
	}
	return c.JSON(http.StatusOK, stats)
}


func getLivestreamStatisticsHandler(c echo.Context) error {
	ctx := c.Request().Context()

	if err := verifyUserSession(c); err != nil {
		return err
	}

	id, err := strconv.Atoi(c.Param("livestream_id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "livestream_id in path must be integer")
	}
	livestreamID := int64(id)

	tx, err := dbConn.BeginTxx(ctx, nil)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to begin transaction: "+err.Error())
	}
	defer tx.Rollback()

	var livestream LivestreamModel
	if err := tx.GetContext(ctx, &livestream, "SELECT * FROM livestreams WHERE id = ?", livestreamID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return echo.NewHTTPError(http.StatusBadRequest, "cannot get stats of not found livestream")
		} else {
			return echo.NewHTTPError(http.StatusInternalServerError, "failed to get livestream: "+err.Error())
		}
	}

	var livestreams []*LivestreamModel
	if err := tx.SelectContext(ctx, &livestreams, "SELECT * FROM livestreams"); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to get livestreams: "+err.Error())
	}

	// ランク算出
	var ranking LivestreamRanking
	for _, livestream := range livestreams {
		var reactions int64
		if err := tx.GetContext(ctx, &reactions, "SELECT COUNT(*) FROM livestreams l INNER JOIN reactions r ON l.id = r.livestream_id WHERE l.id = ?", livestream.ID); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return echo.NewHTTPError(http.StatusInternalServerError, "failed to count reactions: "+err.Error())
		}

		var totalTips int64
		if err := tx.GetContext(ctx, &totalTips, "SELECT IFNULL(SUM(l2.tip), 0) FROM livestreams l INNER JOIN livecomments l2 ON l.id = l2.livestream_id WHERE l.id = ?", livestream.ID); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return echo.NewHTTPError(http.StatusInternalServerError, "failed to count tips: "+err.Error())
		}

		score := reactions + totalTips
		ranking = append(ranking, LivestreamRankingEntry{
			LivestreamID: livestream.ID,
			Score:        score,
		})
	}
	sort.Sort(ranking)

	var rank int64 = 1
	for i := len(ranking) - 1; i >= 0; i-- {
		entry := ranking[i]
		if entry.LivestreamID == livestreamID {
			break
		}
		rank++
	}

	// 視聴者数算出
	var viewersCount int64
	if err := tx.GetContext(ctx, &viewersCount, `SELECT COUNT(*) FROM livestreams l INNER JOIN livestream_viewers_history h ON h.livestream_id = l.id WHERE l.id = ?`, livestreamID); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to count livestream viewers: "+err.Error())
	}

	// 最大チップ額
	var maxTip int64
	if err := tx.GetContext(ctx, &maxTip, `SELECT IFNULL(MAX(tip), 0) FROM livestreams l INNER JOIN livecomments l2 ON l2.livestream_id = l.id WHERE l.id = ?`, livestreamID); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to find maximum tip livecomment: "+err.Error())
	}

	// リアクション数
	var totalReactions int64
	if err := tx.GetContext(ctx, &totalReactions, "SELECT COUNT(*) FROM livestreams l INNER JOIN reactions r ON r.livestream_id = l.id WHERE l.id = ?", livestreamID); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to count total reactions: "+err.Error())
	}

	// スパム報告数
	var totalReports int64
	if err := tx.GetContext(ctx, &totalReports, `SELECT COUNT(*) FROM livestreams l INNER JOIN livecomment_reports r ON r.livestream_id = l.id WHERE l.id = ?`, livestreamID); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to count total spam reports: "+err.Error())
	}

	if err := tx.Commit(); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to commit: "+err.Error())
	}

	return c.JSON(http.StatusOK, LivestreamStatistics{
		Rank:           rank,
		ViewersCount:   viewersCount,
		MaxTip:         maxTip,
		TotalReactions: totalReactions,
		TotalReports:   totalReports,
	})
}
