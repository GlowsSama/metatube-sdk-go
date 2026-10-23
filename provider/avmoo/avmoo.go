package avmoo

import (
	"github.com/metatube-community/metatube-sdk-go/provider"
	"github.com/metatube-community/metatube-sdk-go/provider/internal/avcore"
)

const (
	Name     = "AVMOO"
	Priority = 1000 - 16
	baseURL  = "https://avmoo.shop/"
)

type AVMoo struct {
	*avcore.Provider
}

func New() *AVMoo {
	return &AVMoo{Provider: avcore.New(Name, baseURL, Priority)}
}

func init() {
	provider.Register(Name, New)
}
