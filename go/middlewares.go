package main

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"sync"
)

func appAuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		c, err := r.Cookie("app_session")
		if errors.Is(err, http.ErrNoCookie) || c.Value == "" {
			writeError(w, http.StatusUnauthorized, errors.New("app_session cookie is required"))
			return
		}
		accessToken := c.Value
		user := &User{}
		err = db.GetContext(ctx, user, "SELECT * FROM users WHERE access_token = ?", accessToken)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				writeError(w, http.StatusUnauthorized, errors.New("invalid access token"))
				return
			}
			writeError(w, http.StatusInternalServerError, err)
			return
		}

		ctx = context.WithValue(ctx, "user", user)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func ownerAuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		c, err := r.Cookie("owner_session")
		if errors.Is(err, http.ErrNoCookie) || c.Value == "" {
			writeError(w, http.StatusUnauthorized, errors.New("owner_session cookie is required"))
			return
		}
		accessToken := c.Value
		owner := &Owner{}
		if err := db.GetContext(ctx, owner, "SELECT * FROM owners WHERE access_token = ?", accessToken); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				writeError(w, http.StatusUnauthorized, errors.New("invalid access token"))
				return
			}
			writeError(w, http.StatusInternalServerError, err)
			return
		}

		ctx = context.WithValue(ctx, "owner", owner)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func chairAuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		// クッキーから accessToken を取得
		c, err := r.Cookie("chair_session")
		if errors.Is(err, http.ErrNoCookie) || c.Value == "" {
			writeError(w, http.StatusUnauthorized, errors.New("chair_session cookie is required"))
			return
		}
		accessToken := c.Value

		// キャッシュから Chair を取得
		chairID, found := getChairIDFromAccessToken(accessToken)
		var chair *Chair
		if found {
			// キャッシュに存在する場合、Chair を生成
			chair = &Chair{ID: chairID, AccessToken: accessToken}
		} else {
			// キャッシュに存在しない場合、データベースから取得
			chair = &Chair{}
			err = db.GetContext(ctx, chair, "SELECT * FROM chairs WHERE access_token = ?", accessToken)
			if err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					writeError(w, http.StatusUnauthorized, errors.New("invalid access token"))
					return
				}
				writeError(w, http.StatusInternalServerError, err)
				return
			}
			// キャッシュに保存
			cacheAccessToken(chair.ID, accessToken)
		}

		// コンテキストに Chair を追加
		ctx = context.WithValue(ctx, "chair", chair)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

var AccessTokenCache sync.Map

// accessToken に紐づく Chair ID をキャッシュから取得
func getChairIDFromAccessToken(accessToken string) (string, bool) {
	chairID, ok := AccessTokenCache.Load(accessToken)
	if !ok {
		return "", false
	}
	return chairID.(string), true
}

// Chair ID と accessToken をキャッシュに保存
func cacheAccessToken(chairID, accessToken string) {
	AccessTokenCache.Store(accessToken, chairID)
}
