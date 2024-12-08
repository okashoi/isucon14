package main

import (
	"database/sql"
	"errors"
	"log"
	"time"
)

func matchingAuto() {
	t := time.NewTicker(100 * time.Millisecond)
	for {
		select {
		case <-t.C:
			if err := matchingOne(); err != nil {
				log.Println("matchingAuto() failed", err)
			}
		}
	}
}

func matchingOne() error {
	t := time.Now()

	ride := &Ride{}
	if err := db.Get(ride, `SELECT * FROM rides WHERE chair_id IS NULL ORDER BY created_at LIMIT 1`); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	}

	var matchedChairId string
	// マンハッタン距離が最も近い椅子
	if err := db.Get(&matchedChairId, "SELECT l.chair_id FROM latest_chair_locations AS l INNER JOIN chairs AS c ON l.chair_id = c.id WHERE c.is_active = 1 ORDER BY ABS(latest_latitude - ?) + ABS(latest_longitude - ?) LIMIT 1", ride.PickupLatitude, ride.PickupLongitude); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			log.Println("matchingOne() no chair found")
			return nil
		}
		return err
	}

	if _, err := db.Exec("UPDATE rides SET chair_id = ? WHERE id = ? AND char_id IS NULL", matchedChairId, ride.ID); err != nil {
		return err
	}

	log.Printf("matchingOne() matching done (%d ms)\n", time.Since(t).Milliseconds())

	return nil
}
