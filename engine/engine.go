package engine

import (
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
	"github.com/zhiwei-w-luo/gotradebot/config"

)

// Engine contains configuration, portfolio manager, exchange & ticker data and is the
// overarching type across this code base.
type Engine struct {
	Config                  *config.Config
	connectionManager       *connectionManager
	DatabaseManager         *DatabaseConnectionManager
	Settings                Settings
	uptime                  time.Time
	ServicesWG              sync.WaitGroup
}

// Bot is a happy global engine to allow various areas of the application
// to access its setup services and functions
var Bot *Engine

// New starts a new engine
func New() (*Engine, error) {
	newEngineMutex.Lock()
	defer newEngineMutex.Unlock()
	var b Engine
	b.Config = &config.Cfg

	err := b.Config.LoadConfig("", false)
	if err != nil {
		return nil, fmt.Errorf("failed to load config. Err: %s", err)
	}

	return &b, nil
}

// NewFromSettings starts a new engine based on supplied settings
func NewFromSettings(settings *Settings, flagSet map[string]bool) (*Engine, error) {
	newEngineMutex.Lock()
	defer newEngineMutex.Unlock()
	if settings == nil {
		return nil, errors.New("engine: settings is nil")
	}

	var b Engine
	var err error

	b.Config, err = loadConfigWithSettings(settings, flagSet)
	if err != nil {
		return nil, fmt.Errorf("failed to load config. Err: %w", err)
	}

	if *b.Config.Logging.Enabled {
		err = gctlog.SetupGlobalLogger()
		if err != nil {
			return nil, fmt.Errorf("failed to setup global logger. %w", err)
		}
		err = gctlog.SetupSubLoggers(b.Config.Logging.SubLoggers)
		if err != nil {
			return nil, fmt.Errorf("failed to setup sub loggers. %w", err)
		}
		gctlog.Infoln(gctlog.Global, "Logger initialised.")
	}

	b.Settings.ConfigFile = settings.ConfigFile
	b.Settings.DataDir = b.Config.GetDataPath()
	b.Settings.CheckParamInteraction = settings.CheckParamInteraction

	err = utils.AdjustGoMaxProcs(settings.GoMaxProcs)
	if err != nil {
		return nil, fmt.Errorf("unable to adjust runtime GOMAXPROCS value. Err: %w", err)
	}

	b.gctScriptManager, err = gctscript.NewManager(&b.Config.GCTScript)
	if err != nil {
		return nil, fmt.Errorf("failed to create script manager. Err: %w", err)
	}

	b.ExchangeManager = SetupExchangeManager()

	validateSettings(&b, settings, flagSet)

	return &b, nil
}

// loadConfigWithSettings creates configuration based on the provided settings
func loadConfigWithSettings(settings *Settings, flagSet map[string]bool) (*config.Config, error) {
	filePath, err := config.GetAndMigrateDefaultPath(settings.ConfigFile)
	if err != nil {
		return nil, err
	}
	log.Printf("Loading config file %s..\n", filePath)

	conf := &config.Config{}
	err = conf.ReadConfigFromFile(filePath, settings.EnableDryRun)
	if err != nil {
		return nil, fmt.Errorf(config.ErrFailureOpeningConfig, filePath, err)
	}
	// Apply overrides from settings
	if flagSet["datadir"] {
		// warn if dryrun isn't enabled
		if !settings.EnableDryRun {
			log.Println("Command line argument '-datadir' induces dry run mode.")
		}
		settings.EnableDryRun = true
		conf.DataDirectory = settings.DataDir
	}

	return conf, conf.CheckConfig()
}

// Start starts the engine
func (bot *Engine) Start() error {
	if bot == nil {
		return errors.New("engine instance is nil")
	}
	var err error
	newEngineMutex.Lock()
	defer newEngineMutex.Unlock()

	if bot.Settings.EnableDatabaseManager {
		bot.DatabaseManager, err = SetupDatabaseConnectionManager(&bot.Config.Database)
		if err != nil {
			gctlog.Errorf(gctlog.Global, "Database manager unable to setup: %v", err)
		} else {
			err = bot.DatabaseManager.Start(&bot.ServicesWG)
			if err != nil {
				gctlog.Errorf(gctlog.Global, "Database manager unable to start: %v", err)
			}
		}
	}

	if bot.Settings.EnableDispatcher {
		if err = dispatch.Start(bot.Settings.DispatchMaxWorkerAmount, bot.Settings.DispatchJobsLimit); err != nil {
			gctlog.Errorf(gctlog.DispatchMgr, "Dispatcher unable to start: %v", err)
		}
	}

	// Sets up internet connectivity monitor
	if bot.Settings.EnableConnectivityMonitor {
		bot.connectionManager, err = setupConnectionManager(&bot.Config.ConnectionMonitor)
		if err != nil {
			gctlog.Errorf(gctlog.Global, "Connection manager unable to setup: %v", err)
		} else {
			err = bot.connectionManager.Start()
			if err != nil {
				gctlog.Errorf(gctlog.Global, "Connection manager unable to start: %v", err)
			}
		}
	}

	if bot.Settings.EnableNTPClient {
		if bot.Config.NTPClient.Level == 0 {
			var responseMessage string
			responseMessage, err = bot.Config.SetNTPCheck(os.Stdin)
			if err != nil {
				return fmt.Errorf("unable to set NTP check: %w", err)
			}
			gctlog.Info(gctlog.TimeMgr, responseMessage)
		}
		bot.ntpManager, err = setupNTPManager(&bot.Config.NTPClient, *bot.Config.Logging.Enabled)
		if err != nil {
			gctlog.Errorf(gctlog.Global, "NTP manager unable to start: %s", err)
		}
	}

	bot.uptime = time.Now()
	gctlog.Debugf(gctlog.Global, "Bot '%s' started.\n", bot.Config.Name)
	gctlog.Debugf(gctlog.Global, "Using data dir: %s\n", bot.Settings.DataDir)
	if *bot.Config.Logging.Enabled && strings.Contains(bot.Config.Logging.Output, "file") {
		gctlog.Debugf(gctlog.Global, "Using log file: %s\n",
			filepath.Join(gctlog.LogPath, bot.Config.Logging.LoggerFileConfig.FileName))
	}
	gctlog.Debugf(gctlog.Global,
		"Using %d out of %d logical processors for runtime performance\n",
		runtime.GOMAXPROCS(-1), runtime.NumCPU())

	enabledExchanges := bot.Config.CountEnabledExchanges()
	if bot.Settings.EnableAllExchanges {
		enabledExchanges = len(bot.Config.Exchanges)
	}

	gctlog.Debugln(gctlog.Global, "EXCHANGE COVERAGE")
	gctlog.Debugf(gctlog.Global, "\t Available Exchanges: %d. Enabled Exchanges: %d.\n",
		len(bot.Config.Exchanges), enabledExchanges)

	if bot.Settings.ExchangePurgeCredentials {
		gctlog.Debugln(gctlog.Global, "Purging exchange API credentials.")
		bot.Config.PurgeExchangeAPICredentials()
	}

	gctlog.Debugln(gctlog.Global, "Setting up exchanges..")
	err = bot.SetupExchanges()
	if err != nil {
		return err
	}

	if bot.Settings.EnableCommsRelayer {
		bot.CommunicationsManager, err = SetupCommunicationManager(&bot.Config.Communications)
		if err != nil {
			gctlog.Errorf(gctlog.Global, "Communications manager unable to setup: %s", err)
		} else {
			err = bot.CommunicationsManager.Start()
			if err != nil {
				gctlog.Errorf(gctlog.Global, "Communications manager unable to start: %s", err)
			}
		}
	}

	err = currency.RunStorageUpdater(currency.BotOverrides{
		Coinmarketcap:     bot.Settings.EnableCoinmarketcapAnalysis,
		CurrencyConverter: bot.Settings.EnableCurrencyConverter,
		CurrencyLayer:     bot.Settings.EnableCurrencyLayer,
		ExchangeRates:     bot.Settings.EnableExchangeRates,
		Fixer:             bot.Settings.EnableFixer,
		OpenExchangeRates: bot.Settings.EnableOpenExchangeRates,
		ExchangeRateHost:  bot.Settings.EnableExchangeRateHost,
	},
		&bot.Config.Currency,
		bot.Settings.DataDir)
	if err != nil {
		gctlog.Errorf(gctlog.Global, "ExchangeSettings updater system failed to start %s", err)
	}

	if bot.Settings.EnableGRPC {
		go StartRPCServer(bot)
	}

	if bot.Settings.EnablePortfolioManager {
		if bot.portfolioManager == nil {
			bot.portfolioManager, err = setupPortfolioManager(bot.ExchangeManager, bot.Settings.PortfolioManagerDelay, &bot.Config.Portfolio)
			if err != nil {
				gctlog.Errorf(gctlog.Global, "portfolio manager unable to setup: %s", err)
			} else {
				err = bot.portfolioManager.Start(&bot.ServicesWG)
				if err != nil {
					gctlog.Errorf(gctlog.Global, "portfolio manager unable to start: %s", err)
				}
			}
		}
	}

	if bot.Settings.EnableDataHistoryManager {
		if bot.dataHistoryManager == nil {
			bot.dataHistoryManager, err = SetupDataHistoryManager(bot.ExchangeManager, bot.DatabaseManager, &bot.Config.DataHistoryManager)
			if err != nil {
				gctlog.Errorf(gctlog.Global, "database history manager unable to setup: %s", err)
			} else {
				err = bot.dataHistoryManager.Start()
				if err != nil {
					gctlog.Errorf(gctlog.Global, "database history manager unable to start: %s", err)
				}
			}
		}
	}

	bot.WithdrawManager, err = SetupWithdrawManager(bot.ExchangeManager, bot.portfolioManager, bot.Settings.EnableDryRun)
	if err != nil {
		return err
	}

	if bot.Settings.EnableDeprecatedRPC || bot.Settings.EnableWebsocketRPC {
		var filePath string
		filePath, err = config.GetAndMigrateDefaultPath(bot.Settings.ConfigFile)
		if err != nil {
			return err
		}
		bot.apiServer, err = setupAPIServerManager(&bot.Config.RemoteControl, &bot.Config.Profiler, bot.ExchangeManager, bot, bot.portfolioManager, filePath)
		if err != nil {
			gctlog.Errorf(gctlog.Global, "API Server unable to start: %s", err)
		} else {
			if bot.Settings.EnableDeprecatedRPC {
				err = bot.apiServer.StartRESTServer()
				if err != nil {
					gctlog.Errorf(gctlog.Global, "could not start REST API server: %s", err)
				}
			}
			if bot.Settings.EnableWebsocketRPC {
				err = bot.apiServer.StartWebsocketServer()
				if err != nil {
					gctlog.Errorf(gctlog.Global, "could not start websocket API server: %s", err)
				}
			}
		}
	}

	if bot.Settings.EnableDepositAddressManager {
		bot.DepositAddressManager = SetupDepositAddressManager()
		go func() {
			err = bot.DepositAddressManager.Sync(bot.GetAllExchangeCryptocurrencyDepositAddresses())
			if err != nil {
				gctlog.Errorf(gctlog.Global, "Deposit address manager unable to setup: %s", err)
			}
		}()
	}

	if bot.Settings.EnableOrderManager {
		bot.OrderManager, err = SetupOrderManager(
			bot.ExchangeManager,
			bot.CommunicationsManager,
			&bot.ServicesWG,
			bot.Settings.Verbose)
		if err != nil {
			gctlog.Errorf(gctlog.Global, "Order manager unable to setup: %s", err)
		} else {
			err = bot.OrderManager.Start()
			if err != nil {
				gctlog.Errorf(gctlog.Global, "Order manager unable to start: %s", err)
			}
		}
	}

	if bot.Settings.EnableExchangeSyncManager {
		exchangeSyncCfg := &SyncManagerConfig{
			SynchronizeTicker:       bot.Settings.EnableTickerSyncing,
			SynchronizeOrderbook:    bot.Settings.EnableOrderbookSyncing,
			SynchronizeTrades:       bot.Settings.EnableTradeSyncing,
			SynchronizeContinuously: bot.Settings.SyncContinuously,
			TimeoutREST:             bot.Settings.SyncTimeoutREST,
			TimeoutWebsocket:        bot.Settings.SyncTimeoutWebsocket,
			NumWorkers:              bot.Settings.SyncWorkersCount,
			Verbose:                 bot.Settings.Verbose,
			FiatDisplayCurrency:     bot.Config.Currency.FiatDisplayCurrency,
			PairFormatDisplay:       bot.Config.Currency.CurrencyPairFormat,
		}

		bot.currencyPairSyncer, err = setupSyncManager(
			exchangeSyncCfg,
			bot.ExchangeManager,
			&bot.Config.RemoteControl,
			bot.Settings.EnableWebsocketRoutine)
		if err != nil {
			gctlog.Errorf(gctlog.Global, "Unable to initialise exchange currency pair syncer. Err: %s", err)
		} else {
			go func() {
				err = bot.currencyPairSyncer.Start()
				if err != nil {
					gctlog.Errorf(gctlog.Global, "failed to start exchange currency pair manager. Err: %s", err)
				}
			}()
		}
	}

	if bot.Settings.EnableEventManager {
		bot.eventManager, err = setupEventManager(bot.CommunicationsManager, bot.ExchangeManager, bot.Settings.EventManagerDelay, bot.Settings.EnableDryRun)
		if err != nil {
			gctlog.Errorf(gctlog.Global, "Unable to initialise event manager. Err: %s", err)
		} else {
			err = bot.eventManager.Start()
			if err != nil {
				gctlog.Errorf(gctlog.Global, "failed to start event manager. Err: %s", err)
			}
		}
	}

	if bot.Settings.EnableWebsocketRoutine {
		bot.websocketRoutineManager, err = setupWebsocketRoutineManager(bot.ExchangeManager, bot.OrderManager, bot.currencyPairSyncer, &bot.Config.Currency, bot.Settings.Verbose)
		if err != nil {
			gctlog.Errorf(gctlog.Global, "Unable to initialise websocket routine manager. Err: %s", err)
		} else {
			err = bot.websocketRoutineManager.Start()
			if err != nil {
				gctlog.Errorf(gctlog.Global, "failed to start websocket routine manager. Err: %s", err)
			}
		}
	}

	if bot.Settings.EnableGCTScriptManager {
		bot.gctScriptManager, err = gctscript.NewManager(&bot.Config.GCTScript)
		if err != nil {
			gctlog.Errorf(gctlog.Global, "failed to create script manager. Err: %s", err)
		}
		if err = bot.gctScriptManager.Start(&bot.ServicesWG); err != nil {
			gctlog.Errorf(gctlog.Global, "GCTScript manager unable to start: %s", err)
		}
	}

	if bot.Settings.EnableCurrencyStateManager {
		bot.currencyStateManager, err = SetupCurrencyStateManager(
			bot.Config.CurrencyStateManager.Delay,
			bot.ExchangeManager)
		if err != nil {
			gctlog.Errorf(gctlog.Global,
				"%s unable to setup: %s",
				CurrencyStateManagementName,
				err)
		} else {
			err = bot.currencyStateManager.Start()
			if err != nil {
				gctlog.Errorf(gctlog.Global,
					"%s unable to start: %s",
					CurrencyStateManagementName,
					err)
			}
		}
	}
	return nil
}

// Stop correctly shuts down engine saving configuration files
func (bot *Engine) Stop() {
	newEngineMutex.Lock()
	defer newEngineMutex.Unlock()

	gctlog.Debugln(gctlog.Global, "Engine shutting down..")

	if len(bot.portfolioManager.GetAddresses()) != 0 {
		bot.Config.Portfolio = *bot.portfolioManager.GetPortfolio()
	}

	if bot.gctScriptManager.IsRunning() {
		if err := bot.gctScriptManager.Stop(); err != nil {
			gctlog.Errorf(gctlog.Global, "GCTScript manager unable to stop. Error: %v", err)
		}
	}
	if bot.OrderManager.IsRunning() {
		if err := bot.OrderManager.Stop(); err != nil {
			gctlog.Errorf(gctlog.Global, "Order manager unable to stop. Error: %v", err)
		}
	}
	if bot.eventManager.IsRunning() {
		if err := bot.eventManager.Stop(); err != nil {
			gctlog.Errorf(gctlog.Global, "event manager unable to stop. Error: %v", err)
		}
	}
	if bot.ntpManager.IsRunning() {
		if err := bot.ntpManager.Stop(); err != nil {
			gctlog.Errorf(gctlog.Global, "NTP manager unable to stop. Error: %v", err)
		}
	}
	if bot.CommunicationsManager.IsRunning() {
		if err := bot.CommunicationsManager.Stop(); err != nil {
			gctlog.Errorf(gctlog.Global, "Communication manager unable to stop. Error: %v", err)
		}
	}
	if bot.portfolioManager.IsRunning() {
		if err := bot.portfolioManager.Stop(); err != nil {
			gctlog.Errorf(gctlog.Global, "Fund manager unable to stop. Error: %v", err)
		}
	}
	if bot.connectionManager.IsRunning() {
		if err := bot.connectionManager.Stop(); err != nil {
			gctlog.Errorf(gctlog.Global, "Connection manager unable to stop. Error: %v", err)
		}
	}
	if bot.apiServer.IsRESTServerRunning() {
		if err := bot.apiServer.StopRESTServer(); err != nil {
			gctlog.Errorf(gctlog.Global, "API Server unable to stop REST server. Error: %s", err)
		}
	}
	if bot.apiServer.IsWebsocketServerRunning() {
		if err := bot.apiServer.StopWebsocketServer(); err != nil {
			gctlog.Errorf(gctlog.Global, "API Server unable to stop websocket server. Error: %s", err)
		}
	}
	if bot.dataHistoryManager.IsRunning() {
		if err := bot.dataHistoryManager.Stop(); err != nil {
			gctlog.Errorf(gctlog.DataHistory, "data history manager unable to stop. Error: %v", err)
		}
	}
	if bot.DatabaseManager.IsRunning() {
		if err := bot.DatabaseManager.Stop(); err != nil {
			gctlog.Errorf(gctlog.Global, "Database manager unable to stop. Error: %v", err)
		}
	}
	if dispatch.IsRunning() {
		if err := dispatch.Stop(); err != nil {
			gctlog.Errorf(gctlog.DispatchMgr, "Dispatch system unable to stop. Error: %v", err)
		}
	}
	if bot.websocketRoutineManager.IsRunning() {
		if err := bot.websocketRoutineManager.Stop(); err != nil {
			gctlog.Errorf(gctlog.Global, "websocket routine manager unable to stop. Error: %v", err)
		}
	}
	if bot.currencyStateManager.IsRunning() {
		if err := bot.currencyStateManager.Stop(); err != nil {
			gctlog.Errorf(gctlog.Global,
				"currency state manager unable to stop. Error: %v",
				err)
		}
	}

	if err := currency.ShutdownStorageUpdater(); err != nil {
		gctlog.Errorf(gctlog.Global, "ExchangeSettings storage system. Error: %v", err)
	}

	if !bot.Settings.EnableDryRun {
		err := bot.Config.SaveConfigToFile(bot.Settings.ConfigFile)
		if err != nil {
			gctlog.Errorln(gctlog.Global, "Unable to save config.")
		} else {
			gctlog.Debugln(gctlog.Global, "Config file saved successfully.")
		}
	}

	// Wait for services to gracefully shutdown
	bot.ServicesWG.Wait()
	if err := gctlog.CloseLogger(); err != nil {
		log.Printf("Failed to close logger. Error: %v\n", err)
	}
}


// FlagSet defines set flags from command line args for comparison methods
type FlagSet map[string]bool

// WithBool checks the supplied flag. If set it will overide the config boolean
// value as a command line takes precedence. If not set will fall back to config
// options.
func (f FlagSet) WithBool(key string, flagValue *bool, configValue bool) {
	isSet := f[key]
	*flagValue = !isSet && configValue || isSet && *flagValue
}



