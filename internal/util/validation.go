package util

import (
	"net/http"

	"github.com/go-playground/validator/v10"
	"github.com/labstack/echo/v5"
)

type DefaultValidator struct {
	validator *validator.Validate
}

func (cv *DefaultValidator) Validate(i any) error {
	if err := cv.validator.Struct(i); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	return nil
}

func NewDefaultValidator() echo.Validator {
	return &DefaultValidator{validator: validator.New()}
}
