// Copyright © 2018 the Kudzu contributors.
// Licensed under the Apache License, Version 2.0; see the NOTICE file.

package delegate

import (
	"crypto/sha256"
	"encoding/base32"
	"encoding/json"
	"fmt"
)

type configPair struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

const emptyConfigHash = "empty"

func configHash(conf map[string]string) (string, error) {
	if len(conf) == 0 {
		return emptyConfigHash, nil
	}

	pairs := make([]configPair, 0, len(conf))
	for key, val := range conf {
		pairs = append(pairs, configPair{key, val})
	}

	b, err := json.Marshal(pairs)
	if err != nil {
		return "", err
	}

	sum := sha256.Sum256(b)
	h := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(sum[:])
	return fmt.Sprintf("sha256:%s", h), nil
}
