// Package util provides utility functions and middleware for the Medsenger Neyrox bot.
package util

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"

	"github.com/labstack/echo/v5"
	"github.com/tikhonp/maigo"
)

type agentTokenModel struct {
	AgentToken string `json:"agent_token" validate:"required"`
}

// processAgentToken decodes and validates the Medsenger agent JWT, stashing the
// contract id and the raw token in the echo context for downstream handlers.
func processAgentToken(agentToken string, c *echo.Context, client *maigo.Client, roles []maigo.RequestRole) error {
	data, err := client.DecodeAgentJWT(agentToken)
	if err != nil {
		return echo.NewHTTPError(http.StatusUnauthorized, "Invalid jwt key.")
	}
	// Require the token to carry at least one of the allowed roles.
	if len(roles) > 0 {
		ok := slices.ContainsFunc(roles, func(r maigo.RequestRole) bool {
			return slices.Contains(data.Roles, r)
		})
		if !ok {
			return echo.NewHTTPError(http.StatusUnauthorized, "Invalid jwt key role.")
		}
	}
	if data.ContractID != nil {
		c.Set("contract_id", *data.ContractID)
	}
	c.Set("agent_token", agentToken)
	return nil
}

// AgentTokenJSON reads the token from the JSON request body (Medsenger agent webhooks).
func AgentTokenJSON(client *maigo.Client, roles ...maigo.RequestRole) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			// Buffer the body so the handler can read it again.
			req := c.Request()
			bodyBytes, _ := io.ReadAll(req.Body)
			if err := req.Body.Close(); err != nil {
				return err
			}
			req.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))
			c.SetRequest(req)

			data := new(agentTokenModel)
			if err := json.Unmarshal(bodyBytes, &data); err != nil {
				return echo.NewHTTPError(http.StatusBadRequest, "Invalid JSON.")
			}
			if err := c.Validate(data); err != nil {
				return err
			}
			if err := processAgentToken(data.AgentToken, c, client, roles); err != nil {
				return err
			}
			return next(c)
		}
	}
}

// AgentTokenGetParam reads the token from the ?agent_token= query parameter (page opens).
func AgentTokenGetParam(client *maigo.Client, roles ...maigo.RequestRole) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			agentToken := c.QueryParam("agent_token")
			if err := processAgentToken(agentToken, c, client, roles); err != nil {
				return err
			}
			return next(c)
		}
	}
}

// AgentTokenForm reads the token from the "agent-token" form field (htmx form posts).
func AgentTokenForm(client *maigo.Client, roles ...maigo.RequestRole) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			agentToken := c.FormValue("agent-token")
			if err := processAgentToken(agentToken, c, client, roles); err != nil {
				return err
			}
			return next(c)
		}
	}
}

// GetContractID returns the contract id stashed by one of the AgentToken middlewares.
func GetContractID(c *echo.Context) (int, error) {
	contractID, ok := c.Get("contract_id").(int)
	if ok {
		return contractID, nil
	}
	return 0, errors.New("no contract ID in context")
}

// GetAgentToken returns the raw agent token stashed by one of the AgentToken middlewares.
func GetAgentToken(c *echo.Context) string {
	token, _ := c.Get("agent_token").(string)
	return token
}
