package route

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/metatube-community/metatube-sdk-go/engine"
)

type flareSolverrConfig struct {
	Endpoint string `json:"endpoint"`
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
