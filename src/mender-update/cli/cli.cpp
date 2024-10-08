// Copyright 2023 Northern.tech AS
//
//    Licensed under the Apache License, Version 2.0 (the "License");
//    you may not use this file except in compliance with the License.
//    You may obtain a copy of the License at
//
//        http://www.apache.org/licenses/LICENSE-2.0
//
//    Unless required by applicable law or agreed to in writing, software
//    distributed under the License is distributed on an "AS IS" BASIS,
//    WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
//    See the License for the specific language governing permissions and
//    limitations under the License.

#include <mender-update/cli/cli.hpp>

#ifdef MENDER_EMBED_MENDER_AUTH
#include <mender-auth/cli/cli.hpp>
#endif

#include <iostream>

#include <client_shared/conf.hpp>
#include <common/error.hpp>
#include <common/expected.hpp>

namespace mender {
namespace update {
namespace cli {

namespace conf = mender::client_shared::conf;
namespace error = mender::common::error;
namespace expected = mender::common::expected;

const int NoUpdateInProgressExitStatus = 2;
const int RebootExitStatus = 4;

#ifdef MENDER_EMBED_MENDER_AUTH
const conf::CliCommand cmd_auth {
	.name = "auth",
	.description = "Access built-in mender-auth commands (experimental).",
};
#endif

const conf::CliCommand cmd_check_update {
	.name = "check-update",
	.description = "Force update check",
};

const conf::CliOption opt_stop_before {
	.long_option = "stop-before",
	.description =
		"Stop before entering the given state. "
		"Choices are "
		"`ArtifactInstall_Enter`, "
		"`ArtifactCommit_Enter`, "
		"`ArtifactCommit_Leave`, "
		"`ArtifactRollback_Enter`, "
		"`ArtifactFailure_Enter`, "
		"and `Cleanup`. "
		"You can later resume the installation by using the `resume` command. "
		"Note that the client always stops after `ArtifactInstall` if the update module supports rollback.",
	.parameter = "STATE",
};

const conf::CliCommand cmd_commit {
	.name = "commit",
	.description = "Commit current Artifact. Returns (2) if no update in progress",
	.options =
		{
			opt_stop_before,
		},
};

const conf::CliCommand cmd_daemon {
	.name = "daemon",
	.description = "Start the client as a background service",
};

const conf::CliCommand cmd_install {
	.name = "install",
	.description = "Mender Artifact to install - local file or a URL",
	.argument =
		conf::CliArgument {
			.name = "artifact",
			.mandatory = true,
		},
	.options =
		{
			conf::CliOption {
				.long_option = "reboot-exit-code",
				.description =
					"Return exit code 4 if a manual reboot is required after the Artifact installation.",
			},
			opt_stop_before,
		},
};

const conf::CliCommand cmd_resume {
	.name = "resume",
	.description = "Resume an interrupted installation",
	.options =
		{
			conf::CliOption {
				.long_option = "reboot-exit-code",
				.description =
					"Return exit code 4 if a manual reboot is required after the Artifact installation.",
			},
			opt_stop_before,
		},
};

const conf::CliCommand cmd_rollback {
	.name = "rollback",
	.description = "Rollback current Artifact. Returns (2) if no update in progress",
	.options =
		{
			opt_stop_before,
		},
};

const conf::CliCommand cmd_send_inventory {
	.name = "send-inventory",
	.description = "Force inventory update",
};

const conf::CliCommand cmd_show_artifact {
	.name = "show-artifact",
	.description = "Print the current artifact name to the command line and exit",
};

const conf::CliCommand cmd_show_provides {
	.name = "show-provides",
	.description = "Print the current provides to the command line and exit",
};

const conf::CliApp cli_mender_update = {
	.name = "mender-update",
	.short_description = "manage and start Mender Update",
	.long_description =
		R"(mender-update integrates both the mender-auth daemon and commands for manually
   performing tasks performed by the daemon (see list of COMMANDS below).)",
	.commands =
		{
#ifdef MENDER_EMBED_MENDER_AUTH
			cmd_auth,
#endif
			cmd_check_update,
			cmd_commit,
			cmd_daemon,
			cmd_install,
			cmd_resume,
			cmd_rollback,
			cmd_send_inventory,
			cmd_show_artifact,
			cmd_show_provides,
		},
};

static error::Error CommonInstallFlagsHandler(
	conf::CmdlineOptionsIterator &iter,
	string *filename,
	bool *reboot_exit_code,
	vector<string> *stop_before) {
	while (true) {
		auto arg = iter.Next();
		if (!arg) {
			return arg.error();
		}

		auto value = arg.value();
		if (reboot_exit_code != nullptr and value.option == "--reboot-exit-code") {
			*reboot_exit_code = true;
			continue;
		} else if (stop_before != nullptr and value.option == "--stop-before") {
			if (value.value == "") {
				return conf::MakeError(conf::InvalidOptionsError, "--stop-before needs an argument");
			}
			stop_before->push_back(value.value);
			continue;
		} else if (value.option != "") {
			return conf::MakeError(conf::InvalidOptionsError, "No such option: " + value.option);
		}

		if (value.value != "") {
			if (filename == nullptr or *filename != "") {
				return conf::MakeError(
					conf::InvalidOptionsError, "Too many arguments: " + value.value);
			} else {
				*filename = value.value;
			}
		} else {
			if (filename != nullptr and *filename == "") {
				return conf::MakeError(conf::InvalidOptionsError, "Need a path to an artifact");
			} else {
				break;
			}
		}
	}

	return error::NoError;
}

ExpectedActionPtr ParseUpdateArguments(
	vector<string>::const_iterator start, vector<string>::const_iterator end) {
	if (start == end) {
		return expected::unexpected(conf::MakeError(conf::InvalidOptionsError, "Need an action"));
	}

	bool help_arg = conf::FindCmdlineHelpArg(start + 1, end);
	if (help_arg) {
		conf::PrintCliCommandHelp(cli_mender_update, start[0]);
		return expected::unexpected(error::MakeError(error::ExitWithSuccessError, ""));
	}

	if (start[0] == "show-artifact") {
		conf::CmdlineOptionsIterator iter(start + 1, end, cmd_show_artifact.options);
		auto arg = iter.Next();
		if (!arg) {
			return expected::unexpected(arg.error());
		}

		return make_shared<ShowArtifactAction>();
	} else if (start[0] == "show-provides") {
		conf::CmdlineOptionsIterator iter(start + 1, end, cmd_show_provides.options);
		auto arg = iter.Next();
		if (!arg) {
			return expected::unexpected(arg.error());
		}

		return make_shared<ShowProvidesAction>();
	} else if (start[0] == "install") {
		conf::CmdlineOptionsIterator iter(
			start + 1,
			end,
			cmd_install.options);
		iter.SetArgumentsMode(conf::ArgumentsMode::AcceptBareArguments);

		string filename;
		bool reboot_exit_code = false;
		vector<string> stop_before;
		auto err = CommonInstallFlagsHandler(iter, &filename, &reboot_exit_code, &stop_before);
		if (err != error::NoError) {
			return expected::unexpected(err);
		}

		auto install_action = make_shared<InstallAction>(filename);
		install_action->SetRebootExitCode(reboot_exit_code);
		install_action->SetStopBefore(std::move(stop_before));
		return install_action;
	} else if (start[0] == "resume") {
		conf::CmdlineOptionsIterator iter(
			start + 1,
			end,
			cmd_resume.options);

		bool reboot_exit_code = false;
		vector<string> stop_before;
		auto err = CommonInstallFlagsHandler(iter, nullptr, &reboot_exit_code, &stop_before);
		if (err != error::NoError) {
			return expected::unexpected(err);
		}

		auto resume_action = make_shared<ResumeAction>();
		resume_action->SetRebootExitCode(reboot_exit_code);
		resume_action->SetStopBefore(std::move(stop_before));
		return resume_action;
	} else if (start[0] == "commit") {
		conf::CmdlineOptionsIterator iter(
			start + 1, end, cmd_commit.options);

		vector<string> stop_before;
		auto err = CommonInstallFlagsHandler(iter, nullptr, nullptr, &stop_before);
		if (err != error::NoError) {
			return expected::unexpected(err);
		}

		auto commit_action = make_shared<CommitAction>();
		commit_action->SetStopBefore(std::move(stop_before));
		return commit_action;
	} else if (start[0] == "rollback") {
		conf::CmdlineOptionsIterator iter(
			start + 1, end, cmd_rollback.options);

		vector<string> stop_before;
		auto err = CommonInstallFlagsHandler(iter, nullptr, nullptr, &stop_before);
		if (err != error::NoError) {
			return expected::unexpected(err);
		}

		auto rollback_action = make_shared<RollbackAction>();
		rollback_action->SetStopBefore(std::move(stop_before));
		return rollback_action;
	} else if (start[0] == "daemon") {
		conf::CmdlineOptionsIterator iter(start + 1, end, cmd_daemon.options);
		auto arg = iter.Next();
		if (!arg) {
			return expected::unexpected(arg.error());
		}

		return make_shared<DaemonAction>();
	} else if (start[0] == "send-inventory") {
		conf::CmdlineOptionsIterator iter(start + 1, end, cmd_send_inventory.options);
		auto arg = iter.Next();
		if (!arg) {
			return expected::unexpected(arg.error());
		}

		return make_shared<SendInventoryAction>();
	} else if (start[0] == "check-update") {
		conf::CmdlineOptionsIterator iter(start + 1, end, cmd_check_update.options);
		auto arg = iter.Next();
		if (!arg) {
			return expected::unexpected(arg.error());
		}

		return make_shared<CheckUpdateAction>();
	}
#ifdef MENDER_EMBED_MENDER_AUTH
	// We do not test for this here, because mender-auth has its own Main() function and
	// therefore it is inconvenient (it returns int, not an action). So we do it in Main()
	// below instead, but semantically it is the same as doing it here.
	//
	// else if (start[0] == "auth") {
	// 	...stuff...
#endif
	else {
		return expected::unexpected(
			conf::MakeError(conf::InvalidOptionsError, "No such action: " + start[0]));
	}
}

static error::Error DoMain(
	const vector<string> &args,
	function<void(mender::update::context::MenderContext &ctx)> test_hook) {
	mender::client_shared::conf::MenderConfig config;

	auto args_pos = config.ProcessCmdlineArgs(args.begin(), args.end(), cli_mender_update);
	if (!args_pos) {
		if (args_pos.error().code != error::MakeError(error::ExitWithSuccessError, "").code) {
			conf::PrintCliHelp(cli_mender_update);
		}
		return args_pos.error();
	}

	auto action = ParseUpdateArguments(args.begin() + args_pos.value(), args.end());
	if (!action) {
		if (action.error().code != error::MakeError(error::ExitWithSuccessError, "").code) {
			if (args.size() > 0) {
				conf::PrintCliCommandHelp(cli_mender_update, args[0]);
			} else {
				conf::PrintCliHelp(cli_mender_update);
			}
		}
		return action.error();
	}

	mender::update::context::MenderContext main_context(config);

	test_hook(main_context);

	auto err = main_context.Initialize();
	if (error::NoError != err) {
		return err;
	}

	return action.value()->Execute(main_context);
}

int Main(
	const vector<string> &args,
	function<void(mender::update::context::MenderContext &ctx)> test_hook) {
#ifdef MENDER_EMBED_MENDER_AUTH
	// Early special treatment for "auth" argument.
	if (args.size() > 0 and args[0] == "auth") {
		return mender::auth::cli::Main({args.begin() + 1, args.end()});
	}
#endif

	auto err = DoMain(args, test_hook);

	if (err.code == context::MakeError(context::NoUpdateInProgressError, "").code) {
		return NoUpdateInProgressExitStatus;
	} else if (err.code == context::MakeError(context::RebootRequiredError, "").code) {
		return RebootExitStatus;
	} else if (err != error::NoError) {
		if (err.code == error::MakeError(error::ExitWithSuccessError, "").code) {
			return 0;
		} else if (err.code != error::MakeError(error::ExitWithFailureError, "").code) {
			cerr << "Could not fulfill request: " + err.String() << endl;
		}
		return 1;
	}

	return 0;
}

} // namespace cli
} // namespace update
} // namespace mender
