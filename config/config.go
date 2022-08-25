package config

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/zhiwei-w-luo/gotradebot/database"
	"github.com/zhiwei-w-luo/gotradebot/log"
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
// prestart management of Portfolio, Webserver and Enabled Exchanges
type Config struct {
	Name                 string                    `json:"name"`
	DataDirectory        string                    `json:"dataDirectory"`
	EncryptConfig        int                       `json:"encryptConfig"`
	GlobalHTTPTimeout    time.Duration             `json:"globalHTTPTimeout"`
	Database             database.Config           `json:"database"`
	Logging              log.Config                `json:"logging"`

	// encryption session values
	storedSalt []byte
	sessionDK  []byte


}


// LoadConfig loads your configuration file into your configuration object
func (c *Config) LoadConfig(configPath string, dryrun bool) error {
	err := c.ReadConfigFromFile(configPath, dryrun)
	if err != nil {
		return fmt.Errorf(ErrFailureOpeningConfig, configPath, err)
	}

	return c.CheckConfig()
}

// UpdateConfig updates the config with a supplied config file
func (c *Config) UpdateConfig(configPath string, newCfg *Config, dryrun bool) error {
	err := newCfg.CheckConfig()
	if err != nil {
		return err
	}

	c.Name = newCfg.Name
	c.EncryptConfig = newCfg.EncryptConfig
	c.Currency = newCfg.Currency
	c.GlobalHTTPTimeout = newCfg.GlobalHTTPTimeout
	c.Portfolio = newCfg.Portfolio
	c.Communications = newCfg.Communications
	c.Webserver = newCfg.Webserver
	c.Exchanges = newCfg.Exchanges

	if !dryrun {
		err = c.SaveConfigToFile(configPath)
		if err != nil {
			return err
		}
	}

	return c.LoadConfig(configPath, dryrun)
}

// GetConfig returns a pointer to a configuration object
func GetConfig() *Config {
	return &Cfg
}

// ReadConfigFromFile reads the configuration from the given file
// if target file is encrypted, prompts for encryption key
// Also - if not in dryrun mode - it checks if the configuration needs to be encrypted
// and stores the file as encrypted, if necessary (prompting for enryption key)
func (c *Config) ReadConfigFromFile(configPath string, dryrun bool) error {
	defaultPath, _, err := GetFilePath(configPath)
	if err != nil {
		return err
	}
	confFile, err := os.Open(defaultPath)
	if err != nil {
		return err
	}
	defer confFile.Close()
	result, wasEncrypted, err := ReadConfig(confFile, func() ([]byte, error) { return PromptForConfigKey(false) })
	if err != nil {
		return fmt.Errorf("error reading config %w", err)
	}
	// Override values in the current config
	*c = *result

	if dryrun || wasEncrypted || c.EncryptConfig == fileEncryptionDisabled {
		return nil
	}

	if c.EncryptConfig == fileEncryptionPrompt {
		confirm, err := promptForConfigEncryption()
		if err != nil {
			log.Errorf(log.ConfigMgr, "The encryption prompt failed, ignoring for now, next time we will prompt again. Error: %s\n", err)
			return nil
		}
		if confirm {
			c.EncryptConfig = fileEncryptionEnabled
			return c.SaveConfigToFile(defaultPath)
		}

		c.EncryptConfig = fileEncryptionDisabled
		err = c.SaveConfigToFile(defaultPath)
		if err != nil {
			log.Errorf(log.ConfigMgr, "Cannot save config. Error: %s\n", err)
		}
	}
	return nil
}

// ReadConfig verifies and checks for encryption and loads the config from a JSON object.
// Prompts for decryption key, if target data is encrypted.
// Returns the loaded configuration and whether it was encrypted.
func ReadConfig(configReader io.Reader, keyProvider func() ([]byte, error)) (*Config, bool, error) {
	reader := bufio.NewReader(configReader)

	pref, err := reader.Peek(len(EncryptConfirmString))
	if err != nil {
		return nil, false, err
	}

	if !ConfirmECS(pref) {
		// Read unencrypted configuration
		decoder := json.NewDecoder(reader)
		c := &Config{}
		err = decoder.Decode(c)
		return c, false, err
	}

	conf, err := readEncryptedConfWithKey(reader, keyProvider)
	return conf, true, err
}

// readEncryptedConf reads encrypted configuration and requests key from provider
func readEncryptedConfWithKey(reader *bufio.Reader, keyProvider func() ([]byte, error)) (*Config, error) {
	fileData, err := ioutil.ReadAll(reader)
	if err != nil {
		return nil, err
	}
	for errCounter := 0; errCounter < maxAuthFailures; errCounter++ {
		key, err := keyProvider()
		if err != nil {
			log.Errorf(log.ConfigMgr, "PromptForConfigKey err: %s", err)
			continue
		}

		var c *Config
		c, err = readEncryptedConf(bytes.NewReader(fileData), key)
		if err != nil {
			log.Error(log.ConfigMgr, "Could not decrypt and deserialise data with given key. Invalid password?", err)
			continue
		}
		return c, nil
	}
	return nil, errors.New("failed to decrypt config after 3 attempts")
}

func readEncryptedConf(reader io.Reader, key []byte) (*Config, error) {
	c := &Config{}
	data, err := c.decryptConfigData(reader, key)
	if err != nil {
		return nil, err
	}

	err = json.Unmarshal(data, c)
	return c, err
}

// SaveConfigToFile saves your configuration to your desired path as a JSON object.
// The function encrypts the data and prompts for encryption key, if necessary
func (c *Config) SaveConfigToFile(configPath string) error {
	defaultPath, _, err := GetFilePath(configPath)
	if err != nil {
		return err
	}
	var writer *os.File
	provider := func() (io.Writer, error) {
		writer, err = file.Writer(defaultPath)
		return writer, err
	}
	defer func() {
		if writer != nil {
			err = writer.Close()
			if err != nil {
				log.Error(log.ConfigMgr, err)
			}
		}
	}()
	return c.Save(provider, func() ([]byte, error) { return PromptForConfigKey(true) })
}

// Save saves your configuration to the writer as a JSON object
// with encryption, if configured
// If there is an error when preparing the data to store, the writer is never requested
func (c *Config) Save(writerProvider func() (io.Writer, error), keyProvider func() ([]byte, error)) error {
	payload, err := json.MarshalIndent(c, "", " ")
	if err != nil {
		return err
	}

	if c.EncryptConfig == fileEncryptionEnabled {
		// Ensure we have the key from session or from user
		if len(c.sessionDK) == 0 {
			var key []byte
			key, err = keyProvider()
			if err != nil {
				return err
			}
			var sessionDK, storedSalt []byte
			sessionDK, storedSalt, err = makeNewSessionDK(key)
			if err != nil {
				return err
			}
			c.sessionDK, c.storedSalt = sessionDK, storedSalt
		}
		payload, err = c.encryptConfigFile(payload)
		if err != nil {
			return err
		}
	}
	configWriter, err := writerProvider()
	if err != nil {
		return err
	}
	_, err = io.Copy(configWriter, bytes.NewReader(payload))
	return err
}

// GetFilePath returns the desired config file or the default config file name
// and whether it was loaded from a default location (rather than explicitly specified)
func GetFilePath(configFile string) (configPath string, isImplicitDefaultPath bool, err error) {
	if configFile != "" {
		return configFile, false, nil
	}

	exePath, err := common.GetExecutablePath()
	if err != nil {
		return "", false, err
	}
	newDir := common.GetDefaultDataDir(runtime.GOOS)
	defaultPaths := []string{
		filepath.Join(exePath, File),
		filepath.Join(exePath, EncryptedFile),
		filepath.Join(newDir, File),
		filepath.Join(newDir, EncryptedFile),
	}

	for _, p := range defaultPaths {
		if file.Exists(p) {
			configFile = p
			break
		}
	}
	if configFile == "" {
		return "", false, fmt.Errorf("config.json file not found in %s, please follow README.md in root dir for config generation",
			newDir)
	}

	return configFile, true, nil
}