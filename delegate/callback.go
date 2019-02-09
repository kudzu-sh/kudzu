// Copyright © 2018 the Kudzu contributors.
// Licensed under the Apache License, Version 2.0; see the NOTICE file.

package delegate

import (
	"bytes"
	"fmt"
	"io"
	"net/http"

	"github.com/gorilla/mux"
	"go.uber.org/zap"
)

type CallbackHandler struct {
	Storage ResultStorage
	Log     *zap.Logger
}

func (h *CallbackHandler) Register(router *mux.Router) {
	router.Methods(http.MethodPost).Path("/callbacks/{kind}/{uid}/result").Handler(h)
}

func (h *CallbackHandler) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	defer req.Body.Close()
	vars := mux.Vars(req)
	hash := req.Header.Get("Kudzu-Config-Hash")

	h.Log.Info(
		"Receiving callback",
		zap.String("kind", vars["kind"]),
		zap.String("uid", vars["uid"]),
		zap.String("config_hash", hash),
	)

	if hash == "" {
		http.Error(w, "Missing Kudzu-Config-Hash header", http.StatusBadRequest)
		return
	}

	buf := bytes.Buffer{}
	if _, err := io.Copy(&buf, req.Body); err != nil {
		http.Error(w, fmt.Sprintf("Failed to read body: %v", err), http.StatusInternalServerError)
		return
	}

	key := StorageKey{vars["kind"], vars["uid"]}
	if err := h.Storage.Put(key, hash, buf.Bytes()); err != nil {
		http.Error(w, fmt.Sprintf("Failed to save result: %v", err), http.StatusInternalServerError)
		return
	}

	http.Error(w, "Result saved\n", http.StatusAccepted)
}
