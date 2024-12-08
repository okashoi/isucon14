package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"github.com/jmoiron/sqlx"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"
)

type chairPostChairsRequest struct {
	Name               string `json:"name"`
	Model              string `json:"model"`
	ChairRegisterToken string `json:"chair_register_token"`
}

type chairPostChairsResponse struct {
	ID      string `json:"id"`
	OwnerID string `json:"owner_id"`
}

func chairPostChairs(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	req := &chairPostChairsRequest{}
	if err := bindJSON(r, req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if req.Name == "" || req.Model == "" || req.ChairRegisterToken == "" {
		writeError(w, http.StatusBadRequest, errors.New("some of required fields(name, model, chair_register_token) are empty"))
		return
	}

	owner := &Owner{}
	if err := db.GetContext(ctx, owner, "SELECT * FROM owners WHERE chair_register_token = ?", req.ChairRegisterToken); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusUnauthorized, errors.New("invalid chair_register_token"))
			return
		}
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	chairID := ulid.Make().String()
	accessToken := secureRandomStr(32)

	_, err := db.ExecContext(
		ctx,
		"INSERT INTO chairs (id, owner_id, name, model, is_active, access_token) VALUES (?, ?, ?, ?, ?, ?)",
		chairID, owner.ID, req.Name, req.Model, false, accessToken,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	http.SetCookie(w, &http.Cookie{
		Path:  "/",
		Name:  "chair_session",
		Value: accessToken,
	})

	writeJSON(w, http.StatusCreated, &chairPostChairsResponse{
		ID:      chairID,
		OwnerID: owner.ID,
	})
}

type postChairActivityRequest struct {
	IsActive bool `json:"is_active"`
}

func chairPostActivity(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	chair := ctx.Value("chair").(*Chair)

	req := &postChairActivityRequest{}
	if err := bindJSON(r, req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	_, err := db.ExecContext(ctx, "UPDATE chairs SET is_active = ? WHERE id = ?", req.IsActive, chair.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

type chairPostCoordinateResponse struct {
	RecordedAt int64 `json:"recorded_at"`
}

func chairPostCoordinate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	req := &Coordinate{}
	if err := bindJSON(r, req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	chair := ctx.Value("chair").(*Chair)

	tx, err := db.Beginx()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	defer tx.Rollback()

	chairLocationID := ulid.Make().String()
	if _, err := tx.ExecContext(
		ctx,
		`INSERT INTO chair_locations (id, chair_id, latitude, longitude) VALUES (?, ?, ?, ?)`,
		chairLocationID, chair.ID, req.Latitude, req.Longitude,
	); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	location := &ChairLocation{}
	if err := tx.GetContext(ctx, location, `SELECT * FROM chair_locations WHERE id = ?`, chairLocationID); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	ride := &Ride{}
	if err := tx.GetContext(ctx, ride, `SELECT * FROM rides WHERE chair_id = ? ORDER BY updated_at DESC LIMIT 1`, chair.ID); err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
	} else {
		status, err := getLatestRideStatus(ctx, tx, ride.ID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		if status != "COMPLETED" && status != "CANCELED" {
			if req.Latitude == ride.PickupLatitude && req.Longitude == ride.PickupLongitude && status == "ENROUTE" {
				if _, err := tx.ExecContext(ctx, "INSERT INTO ride_statuses (id, ride_id, status) VALUES (?, ?, ?)", ulid.Make().String(), ride.ID, "PICKUP"); err != nil {
					writeError(w, http.StatusInternalServerError, err)
					return
				}
			}

			if req.Latitude == ride.DestinationLatitude && req.Longitude == ride.DestinationLongitude && status == "CARRYING" {
				if _, err := tx.ExecContext(ctx, "INSERT INTO ride_statuses (id, ride_id, status) VALUES (?, ?, ?)", ulid.Make().String(), ride.ID, "ARRIVED"); err != nil {
					writeError(w, http.StatusInternalServerError, err)
					return
				}
			}
		}
	}

	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	writeJSON(w, http.StatusOK, &chairPostCoordinateResponse{
		RecordedAt: location.CreatedAt.UnixMilli(),
	})
}

// グローバル変数
var (
	bufferLock    sync.Mutex
	coordinateBuf []*CoordinateBF
)

// HTTPリクエストを処理
func chairPostCoordinateBF(w http.ResponseWriter, r *http.Request) {
	req := &Coordinate{}
	if err := bindJSON(r, req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	// 現在のタイムスタンプを生成
	latestTimestamp := time.Now()

	// バッファにデータを追加（タイムスタンプ込み）
	bufferLock.Lock()
	coordinateBuf = append(coordinateBuf, &CoordinateBF{
		Latitude:  req.Latitude,
		Longitude: req.Longitude,
		CreatedAt: latestTimestamp, // ここで作成時刻を保持
	})
	bufferLock.Unlock()

	// レスポンスを返す
	writeJSON(w, http.StatusOK, &chairPostCoordinateResponse{
		RecordedAt: latestTimestamp.UnixMilli(),
	})
}

// 座標データをバルクインサート
func bulkInsertCoordinates(ctx context.Context, tx *sqlx.Tx, coordinates []*CoordinateBF) error {
	if len(coordinates) == 0 {
		return nil
	}

	// INSERT 文を構築
	query := `
        INSERT INTO chair_locations (id, chair_id, latitude, longitude, created_at)
        VALUES `
	values := []interface{}{}
	for _, coord := range coordinates {
		query += "(?, ?, ?, ?, ?),"
		values = append(values, ulid.Make().String(), "chair_id_placeholder", coord.Latitude, coord.Longitude, coord.CreatedAt)
	}
	query = query[:len(query)-1] // 最後のカンマを削除

	// クエリを実行
	_, err := tx.ExecContext(ctx, query, values...)
	return err
}

// 定期的にバッファ内のデータを処理
func startBufferProcessor() {
	ctx := context.Background()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	log.Println("Starting buffer processor.")
	for {
		select {
		case <-ctx.Done():
			log.Println("Buffer processor stopped.")
			return
		case <-ticker.C:
			saveBufferedCoordinates(ctx)
		}
	}
}

// バッファ内のデータを保存し、関連する rides を処理
func saveBufferedCoordinates(ctx context.Context) {
	bufferLock.Lock()
	defer bufferLock.Unlock()

	if len(coordinateBuf) == 0 {
		return
	}

	// バッファの内容をコピーしてクリア
	toSave := coordinateBuf
	coordinateBuf = nil

	// トランザクションを開始
	tx, err := db.Beginx()
	if err != nil {
		log.Printf("Failed to begin transaction: %v", err)
		return
	}
	defer func() {
		if err := tx.Rollback(); err != nil && err != sql.ErrTxDone {
			log.Printf("Failed to rollback transaction: %v", err)
		}
	}()

	// 座標データをまとめて挿入
	if err := bulkInsertCoordinates(ctx, tx, toSave); err != nil {
		log.Printf("Failed to bulk insert coordinates: %v", err)
		return
	}

	// rides の処理
	for _, coord := range toSave {
		ride := &Ride{}
		err := tx.GetContext(ctx, ride, `
            SELECT * FROM rides 
            WHERE chair_id = ? 
            ORDER BY updated_at DESC LIMIT 1`, "chair_id_placeholder")
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			log.Printf("Failed to get latest ride: %v", err)
			return
		}

		// ride が見つかった場合、ステータスを確認
		if err == nil {
			status, err := getLatestRideStatus(ctx, tx, ride.ID)
			if err != nil {
				log.Printf("Failed to get latest ride status: %v", err)
				return
			}

			// ステータスに応じた更新処理
			if status != "COMPLETED" && status != "CANCELED" {
				// Pickup location と一致する場合
				if coord.Latitude == ride.PickupLatitude && coord.Longitude == ride.PickupLongitude && status == "ENROUTE" {
					if _, err := tx.ExecContext(ctx, `
                        INSERT INTO ride_statuses (id, ride_id, status) 
                        VALUES (?, ?, ?)`,
						ulid.Make().String(), ride.ID, "PICKUP"); err != nil {
						log.Printf("Failed to insert ride status: %v", err)
						return
					}
				}

				// Destination location と一致する場合
				if coord.Latitude == ride.DestinationLatitude && coord.Longitude == ride.DestinationLongitude && status == "CARRYING" {
					if _, err := tx.ExecContext(ctx, `
                        INSERT INTO ride_statuses (id, ride_id, status) 
                        VALUES (?, ?, ?)`,
						ulid.Make().String(), ride.ID, "ARRIVED"); err != nil {
						log.Printf("Failed to insert ride status: %v", err)
						return
					}
				}
			}
		}
	}

	// コミット
	if err := tx.Commit(); err != nil {
		log.Printf("Failed to commit transaction: %v", err)
		return
	}

	log.Printf("Saved %d coordinates with ride status updates.", len(toSave))
}

type simpleUser struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type chairGetNotificationResponse struct {
	Data         *chairGetNotificationResponseData `json:"data"`
	RetryAfterMs int                               `json:"retry_after_ms"`
}

type chairGetNotificationResponseData struct {
	RideID                string     `json:"ride_id"`
	User                  simpleUser `json:"user"`
	PickupCoordinate      Coordinate `json:"pickup_coordinate"`
	DestinationCoordinate Coordinate `json:"destination_coordinate"`
	Status                string     `json:"status"`
}

func chairGetNotification(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	chair := ctx.Value("chair").(*Chair)

	tx, err := db.Beginx()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	defer tx.Rollback()
	ride := &Ride{}
	yetSentRideStatus := RideStatus{}
	status := ""

	if err := tx.GetContext(ctx, ride, `SELECT * FROM rides WHERE chair_id = ? ORDER BY updated_at DESC LIMIT 1`, chair.ID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, http.StatusOK, &chairGetNotificationResponse{
				RetryAfterMs: 500,
			})
			return
		}
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	if err := tx.GetContext(ctx, &yetSentRideStatus, `SELECT * FROM ride_statuses WHERE ride_id = ? AND chair_sent_at IS NULL ORDER BY created_at ASC LIMIT 1`, ride.ID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			status, err = getLatestRideStatus(ctx, tx, ride.ID)
			if err != nil {
				writeError(w, http.StatusInternalServerError, err)
				return
			}
		} else {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
	} else {
		status = yetSentRideStatus.Status
	}

	user := &User{}
	err = tx.GetContext(ctx, user, "SELECT * FROM users WHERE id = ? FOR SHARE", ride.UserID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	if yetSentRideStatus.ID != "" {
		_, err := tx.ExecContext(ctx, `UPDATE ride_statuses SET chair_sent_at = CURRENT_TIMESTAMP(6) WHERE id = ?`, yetSentRideStatus.ID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
	}

	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	writeJSON(w, http.StatusOK, &chairGetNotificationResponse{
		Data: &chairGetNotificationResponseData{
			RideID: ride.ID,
			User: simpleUser{
				ID:   user.ID,
				Name: fmt.Sprintf("%s %s", user.Firstname, user.Lastname),
			},
			PickupCoordinate: Coordinate{
				Latitude:  ride.PickupLatitude,
				Longitude: ride.PickupLongitude,
			},
			DestinationCoordinate: Coordinate{
				Latitude:  ride.DestinationLatitude,
				Longitude: ride.DestinationLongitude,
			},
			Status: status,
		},
		RetryAfterMs: 500,
	})
}

type postChairRidesRideIDStatusRequest struct {
	Status string `json:"status"`
}

func chairPostRideStatus(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	rideID := r.PathValue("ride_id")

	chair := ctx.Value("chair").(*Chair)

	req := &postChairRidesRideIDStatusRequest{}
	if err := bindJSON(r, req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	tx, err := db.Beginx()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	defer tx.Rollback()

	ride := &Ride{}
	if err := tx.GetContext(ctx, ride, "SELECT * FROM rides WHERE id = ? FOR UPDATE", rideID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, errors.New("ride not found"))
			return
		}
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	if ride.ChairID.String != chair.ID {
		writeError(w, http.StatusBadRequest, errors.New("not assigned to this ride"))
		return
	}

	switch req.Status {
	// Acknowledge the ride
	case "ENROUTE":
		if _, err := tx.ExecContext(ctx, "INSERT INTO ride_statuses (id, ride_id, status) VALUES (?, ?, ?)", ulid.Make().String(), ride.ID, "ENROUTE"); err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
	// After Picking up user
	case "CARRYING":
		status, err := getLatestRideStatus(ctx, tx, ride.ID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		if status != "PICKUP" {
			writeError(w, http.StatusBadRequest, errors.New("chair has not arrived yet"))
			return
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO ride_statuses (id, ride_id, status) VALUES (?, ?, ?)", ulid.Make().String(), ride.ID, "CARRYING"); err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
	default:
		writeError(w, http.StatusBadRequest, errors.New("invalid status"))
	}

	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
