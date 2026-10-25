package route

import (
	"net/http"
	"sort"

	"github.com/gin-gonic/gin"

	"github.com/metatube-community/metatube-sdk-go/engine"
)

type flareSolverrConfig struct {
	Endpoint string `json:"endpoint"`
}

type domainsConfig struct {
	Providers map[string][]string `json:"providers" binding:"required"`
}

func setFlareSolverr(app *engine.Engine) gin.HandlerFunc {
	return func(c *gin.Context) {
		config := &flareSolverrConfig{}
		if err := c.ShouldBindJSON(config); err != nil {
			abortWithStatusMessage(c, http.StatusBadRequest, err)
			return
		}

		providers, err := app.SetFlareSolverr(config.Endpoint)
		if err != nil {
			abortWithStatusMessage(c, http.StatusBadRequest, err)
			return
		}

		c.JSON(http.StatusOK, &responseMessage{
			Data: gin.H{
				"endpoint":  config.Endpoint,
				"providers": providers,
			},
		})
	}
}

func setDomains(app *engine.Engine) gin.HandlerFunc {
	return func(c *gin.Context) {
		config := &domainsConfig{}
		if err := c.ShouldBindJSON(config); err != nil {
			abortWithStatusMessage(c, http.StatusBadRequest, err)
			return
		}

		names := make([]string, 0, len(config.Providers))
		for name := range config.Providers {
			names = append(names, name)
		}
		sort.Strings(names)

		normalized := make(map[string][]string, len(config.Providers))
		for _, name := range names {
			domains, err := app.SetProviderDomains(name, config.Providers[name])
			if err != nil {
				abortWithStatusMessage(c, http.StatusBadRequest, err)
				return
			}
			normalized[name] = domains
		}

		c.JSON(http.StatusOK, &responseMessage{
			Data: gin.H{"providers": normalized},
		})
	}
}
