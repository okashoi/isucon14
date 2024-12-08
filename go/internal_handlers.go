package main

import (
	"net/http"
)

// このAPIをインスタンス内から一定間隔で叩かせることで、椅子とライドをマッチングさせる
func internalGetMatching(w http.ResponseWriter, r *http.Request) {
	// Go側で定期的にやるようにしたのでここでは何もしない

	w.WriteHeader(http.StatusNoContent)
}
