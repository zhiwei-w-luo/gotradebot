package config

import (
	"errors"
	"sync"
	"time"

	"github.com/zhiwei-w-luo/gotradebot/database"
)

// Constants declared here are filename strings and test strings
const (
	FXProviderFixer                      = "fixer"
	EncryptedFile                        = "config.dat"
	File                                 = "config.json"
	TestFile                             = "../testdata/configtest.json"
	fileEncryptionPrompt                 = 0
	fileEncryptionEnabled                = 1
	fileEncryptionDisabled               = -1
	pairsLastUpdatedWarningThreshold     = 30 // 30 days
	defaultHTTPTimeout                   = time.Second * 15
	defaultWebsocketResponseCheckTimeout = time.Millisecond * 30
	defaultWebsocketResponseMaxLimit     = time.Second * 7
	defaultWebsocketOrderbookBufferLimit = 5
	defaultWebsocketTrafficTimeout       = time.Second * 30
	maxAuthFailures                      = 3
	defaultNTPAllowedDifference          = 50000000
	defaultNTPAllowedNegativeDifference  = 50000000
	DefaultAPIKey                        = "Key"
	DefaultAPISecret                     = "Secret"
	DefaultAPIClientID                   = "ClientID"
	defaultDataHistoryMonitorCheckTimer  = time.Minute
	defaultCurrencyStateManagerDelay     = time.Minute
	defaultMaxJobsPerCycle               = 5
	DefaultOrderbookPublishPeriod        = time.Second * 10
)

// Constants here hold some messages
const (
	ErrExchangeNameEmpty                       = "exchange #%d name is empty"
	ErrNoEnabledExchanges                      = "no exchanges enabled"
	ErrFailureOpeningConfig                    = "fatal error opening %s file. Error: %s"
	ErrCheckingConfigValues                    = "fatal error checking config values. Error: %s"
	WarningExchangeAuthAPIDefaultOrEmptyValues = "exchange %s authenticated API support disabled due to default/empty APIKey/Secret/ClientID values"
	WarningPairsLastUpdatedThresholdExceeded   = "exchange %s last manual update of available currency pairs has exceeded %d days. Manual update required!"
)

// Constants here define unset default values displayed in the config.json
// file
const (
	APIURLNonDefaultMessage              = "NON_DEFAULT_HTTP_LINK_TO_EXCHANGE_API"
	WebsocketURLNonDefaultMessage        = "NON_DEFAULT_HTTP_LINK_TO_WEBSOCKET_EXCHANGE_API"
	DefaultUnsetAPIKey                   = "Key"
	DefaultUnsetAPISecret                = "Secret"
	DefaultUnsetAccountPlan              = "accountPlan"
	DefaultForexProviderExchangeRatesAPI = "ExchangeRateHost"
)

// Variables here are used for configuration
var (
	Cfg                 Config
	m                   sync.Mutex
	ErrExchangeNotFound = errors.New("exchange not found")
)

// Config is the overarching object that holds all the information for
// prestart management of Portfolio, Communications, Webserver and Enabled
// Exchanges
type Config struct {
	Name                 string                    `json:"name"`
	DataDirectory        string                    `json:"dataDirectory"`
	EncryptConfig        int                       `json:"encryptConfig"`
	GlobalHTTPTimeout    time.Duration             `json:"globalHTTPTimeout"`
	Database             database.Config           `json:"database"`
	Logging              log.Config                `json:"logging"`
	ConnectionMonitor    ConnectionMonitorConfig   `json:"connectionMonitor"`
	DataHistoryManager   DataHistoryManager        `json:"dataHistoryManager"`
	CurrencyStateManager CurrencyStateManager      `json:"currencyStateManager"`
	Profiler             Profiler                  `json:"profiler"`
	NTPClient            NTPClientConfig           `json:"ntpclient"`
	GCTScript            gctscript.Config          `json:"gctscript"`
	Currency             currency.Config           `json:"currencyConfig"`
	Communications       base.CommunicationsConfig `json:"communications"`
	RemoteControl        RemoteControlConfig       `json:"remoteControl"`
	Portfolio            portfolio.Base            `json:"portfolioAddresses"`
	Exchanges            []Exchange                `json:"exchanges"`
	BankAccounts         []banking.Account         `json:"bankAccounts"`

	// Deprecated config settings, will be removed at a future date
	Webserver           *WebserverConfig      `json:"webserver,omitempty"`
	CurrencyPairFormat  *currency.PairFormat  `json:"currencyPairFormat,omitempty"`
	FiatDisplayCurrency *currency.Code        `json:"fiatDispayCurrency,omitempty"`
	Cryptocurrencies    *currency.Currencies  `json:"cryptocurrencies,omitempty"`
	SMS                 *base.SMSGlobalConfig `json:"smsGlobal,omitempty"`
	// encryption session values
	storedSalt []byte
	sessionDK  []byte
}

