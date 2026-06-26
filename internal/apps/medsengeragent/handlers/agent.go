package handlers

import (
	"net/http"

	"github.com/getsentry/sentry-go"
	"github.com/labstack/echo/v5"
	"github.com/tikhonp/maigo"
	"github.com/tikhonp/medsenger-neyrox-bot/internal/util"
)

type initModel struct {
	ClinicID  int    `json:"clinic_id" validate:"required"`
	AgentName string `json:"agent_name" validate:"required"`
	Locale    string `json:"locale" validate:"required"`
}

// fetchContractDataOnInit runs after /init to enrich the contract with patient
// metadata and prompt the patient to connect their Neyrox account.
func (mah *MedsengerAgentHandler) fetchContractDataOnInit(contractID int, ctx *echo.Context) {
	ci, err := mah.Maigo.GetContractInfo(contractID)
	if err != nil {
		sentry.CaptureException(err)
		ctx.Logger().Error(err.Error())
		return
	}
	if err := mah.DB.Contracts().UpdateContractWithPatientData(contractID, ci.PatientName, ci.PatientEmail); err != nil {
		sentry.CaptureException(err)
		ctx.Logger().Error(err.Error())
		return
	}
	_, err = mah.Maigo.SendMessage(
		contractID,
		"Подключён агент Neyrox! Пожалуйста, подключите свой аккаунт умного браслета Neyrox.",
		maigo.WithAction("Подключить", "/medsenger/settings", maigo.Action),
		maigo.OnlyPatient(),
	)
	if err != nil {
		sentry.CaptureException(err)
		ctx.Logger().Error(err.Error())
	}
}

func (mah *MedsengerAgentHandler) Init(c *echo.Context) error {
	m := new(initModel)
	if err := c.Bind(m); err != nil {
		return err
	}
	if err := c.Validate(m); err != nil {
		return err
	}
	contractID, err := util.GetContractID(c)
	if err != nil {
		return err
	}
	if err := mah.DB.Contracts().NewContract(contractID, m.ClinicID, m.Locale); err != nil {
		return err
	}
	go mah.fetchContractDataOnInit(contractID, c)
	return c.String(http.StatusCreated, "ok")
}

type statusResponseModel struct {
	IsTrackingData     bool     `json:"is_tracking_data"`
	SupportedScenarios []string `json:"supported_scenarios"`
	TrackedContracts   []int    `json:"tracked_contracts"`
}

func (mah *MedsengerAgentHandler) Status(c *echo.Context) error {
	trackedContracts, err := mah.DB.Contracts().GetActiveContractIds()
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, statusResponseModel{
		IsTrackingData:     true,
		SupportedScenarios: []string{},
		TrackedContracts:   trackedContracts,
	})
}

func (mah *MedsengerAgentHandler) Remove(c *echo.Context) error {
	contractID, err := util.GetContractID(c)
	if err != nil {
		return err
	}
	if err := mah.DB.Contracts().MarkInactiveContractWithID(contractID); err != nil {
		return err
	}
	return c.String(http.StatusCreated, "ok")
}
