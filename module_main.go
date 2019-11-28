package maddy

import (
	"fmt"
	"io"

	"github.com/foxcpp/maddy/config"
	"github.com/foxcpp/maddy/log"
	"github.com/foxcpp/maddy/module"

	// Import packages for side-effect of module registration.
	_ "github.com/foxcpp/maddy/auth/external"
	_ "github.com/foxcpp/maddy/auth/pam"
	_ "github.com/foxcpp/maddy/auth/shadow"
	_ "github.com/foxcpp/maddy/check/command"
	_ "github.com/foxcpp/maddy/check/dkim"
	_ "github.com/foxcpp/maddy/check/dns"
	_ "github.com/foxcpp/maddy/check/dnsbl"
	_ "github.com/foxcpp/maddy/check/spf"
	_ "github.com/foxcpp/maddy/endpoint/imap"
	_ "github.com/foxcpp/maddy/endpoint/smtp"
	_ "github.com/foxcpp/maddy/modify"
	_ "github.com/foxcpp/maddy/modify/dkim"
	_ "github.com/foxcpp/maddy/storage/sql"
	_ "github.com/foxcpp/maddy/target/queue"
	_ "github.com/foxcpp/maddy/target/remote"
	_ "github.com/foxcpp/maddy/target/smtp_downstream"
)

func moduleMain(cfg []config.Node) error {
	globals := config.NewMap(nil, &config.Node{Children: cfg})
	globals.String("state", false, false, DefaultStateDirectory, &config.StateDirectory)
	globals.String("runtime", false, false, DefaultRuntimeDirectory, &config.RuntimeDirectory)
	globals.String("hostname", false, false, "", nil)
	globals.String("autogenerated_msg_domain", false, false, "", nil)
	globals.Custom("tls", false, false, nil, config.TLSDirective, nil)
	globals.Bool("storage_perdomain", false, false, nil)
	globals.Bool("auth_perdomain", false, false, nil)
	globals.StringList("auth_domains", false, false, nil, nil)
	globals.Custom("log", false, false, defaultLogOutput, logOutput, &log.DefaultLogger.Out)
	globals.Bool("debug", false, log.DefaultLogger.Debug, &log.DefaultLogger.Debug)
	globals.AllowUnknown()
	unknown, err := globals.Process()
	if err != nil {
		return err
	}

	if err := InitDirs(); err != nil {
		return err
	}

	defer log.DefaultLogger.Out.Close()

	insts, err := instancesFromConfig(globals.Values, unknown)
	if err != nil {
		return err
	}

	systemdStatus(SDReady, "Listening for incoming connections...")

	handleSignals()

	systemdStatus(SDStopping, "Waiting for running transactions to complete...")

	for _, inst := range insts {
		if closer, ok := inst.(io.Closer); ok {
			if err := closer.Close(); err != nil {
				log.Printf("module %s (%s) close failed: %v", inst.Name(), inst.InstanceName(), err)
			}
		}
	}

	return nil
}

type modInfo struct {
	instance module.Module
	cfg      config.Node
}

func instancesFromConfig(globals map[string]interface{}, nodes []config.Node) ([]module.Module, error) {
	var (
		endpoints []modInfo
		mods      = make([]modInfo, 0, len(nodes))
	)

	for _, block := range nodes {
		var instName string
		var modAliases []string
		if len(block.Args) == 0 {
			instName = block.Name
		} else {
			instName = block.Args[0]
			modAliases = block.Args[1:]
		}

		modName := block.Name

		endpFactory := module.GetEndpoint(modName)
		if endpFactory != nil {
			inst, err := endpFactory(modName, block.Args)
			if err != nil {
				return nil, err
			}

			endpoints = append(endpoints, modInfo{instance: inst, cfg: block})
			continue
		}

		factory := module.Get(modName)
		if factory == nil {
			return nil, config.NodeErr(&block, "unknown module or global directive: %s", modName)
		}

		if module.HasInstance(instName) {
			return nil, config.NodeErr(&block, "config block named %s already exists", instName)
		}

		inst, err := factory(modName, instName, modAliases, nil)
		if err != nil {
			return nil, err
		}

		block := block
		module.RegisterInstance(inst, config.NewMap(globals, &block))
		for _, alias := range modAliases {
			if module.HasInstance(alias) {
				return nil, config.NodeErr(&block, "config block named %s already exists", alias)
			}
			module.RegisterAlias(alias, instName)
		}
		mods = append(mods, modInfo{instance: inst, cfg: block})
	}

	if len(endpoints) == 0 {
		return nil, fmt.Errorf("at least one endpoint should be configured")
	}

	for _, endp := range endpoints {
		if err := endp.instance.Init(config.NewMap(globals, &endp.cfg)); err != nil {
			return nil, err
		}
	}

	for _, inst := range mods {
		if module.Initialized[inst.instance.InstanceName()] {
			continue
		}

		return nil, fmt.Errorf("Unused configuration block at %s:%d - %s (%s)",
			inst.cfg.File, inst.cfg.Line, inst.instance.InstanceName(), inst.instance.Name())
	}

	res := make([]module.Module, 0, len(mods)+len(endpoints))
	for _, endp := range endpoints {
		res = append(res, endp.instance)
	}
	for _, mod := range mods {
		res = append(res, mod.instance)
	}
	return res, nil
}
