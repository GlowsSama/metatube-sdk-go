package avsox

import (
	"github.com/metatube-community/metatube-sdk-go/provider"
	"github.com/metatube-community/metatube-sdk-go/provider/internal/avcore"
)

const (
	Name     = "AVSOX"
	Priority = 1000 - 17
	baseURL  = "https://avsox.click/"
)

type AVSOX struct {
	*avcore.Provider
}

func New() *AVSOX {
	return &AVSOX{Provider: avcore.New(Name, baseURL, Priority)}
}

func init() {
	provider.Register(Name, New)
}
