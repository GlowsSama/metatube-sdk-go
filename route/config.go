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
	Providers      map[string][]string `json:"providers,omitempty"`
	PrimaryDomains map[string]string   `json:"primary_domains,omitempty"`
}

type javDBSessionConfig struct {
	Username      string `json:"username"`
	Password      string `json:"password"`
	Cookie        string `json:"cookie"`
	Authorization string `json:"authorization"`
	APIURL        string `json:"api_url"`
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

		nameSet := make(map[string]struct{}, len(config.Providers)+len(config.PrimaryDomains))
		for name := range config.Providers {
			nameSet[name] = struct{}{}
		}
		for name := range config.PrimaryDomains {
			nameSet[name] = struct{}{}
		}
		names := make([]string, 0, len(nameSet))
		for name := range nameSet {
			names = append(names, name)
		}
		sort.Strings(names)

		normalizedPrimary := make(map[string]string, len(config.PrimaryDomains))
		normalized := make(map[string][]string, len(config.Providers))
		for _, name := range names {
			var (
				primaryInput *string
				backupsInput *[]string
			)
			if primary, ok := config.PrimaryDomains[name]; ok {
				primaryInput = &primary
			}
			if backups, ok := config.Providers[name]; ok {
				backupsInput = &backups
			}

			primary, domains, err := app.SetProviderDomainConfig(name, primaryInput, backupsInput)
			if err != nil {
				abortWithStatusMessage(c, http.StatusBadRequest, err)
				return
			}
			if primaryInput != nil {
				normalizedPrimary[name] = primary
			}
			if backupsInput != nil {
				normalized[name] = domains
			}
		}

		c.JSON(http.StatusOK, &responseMessage{
			Data: gin.H{
				"providers":       normalized,
				"primary_domains": normalizedPrimary,
			},
		})
	}
}

func setJavDBSession(app *engine.Engine) gin.HandlerFunc {
	return func(c *gin.Context) {
		config := &javDBSessionConfig{}
		if err := c.ShouldBindJSON(config); err != nil {
			abortWithStatusMessage(c, http.StatusBadRequest, err)
			return
		}

		status, err := app.SetJavDBSessionWithAPI(config.Username, config.Password, config.Cookie,
			config.Authorization, config.APIURL)
		if err != nil {
			abortWithStatusMessage(c, http.StatusBadRequest, err)
			return
		}
		c.JSON(http.StatusOK, &responseMessage{Data: status})
	}
}
