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

#include <common/processes.hpp>

#include <sys/stat.h>
#include <gtest/gtest.h>
#include <gmock/gmock.h>

#include <fstream>

namespace procs = mender::common::processes;

using namespace std;

class ProcessesTests : public testing::Test {
protected:
	const char *test_script_fname = "./test_script.sh";

	bool PrepareTestScript(const string script) {
		ofstream os(test_script_fname);
		os << script;
		os.close();

		int ret = chmod(test_script_fname, S_IRUSR | S_IWUSR | S_IXUSR);
		return ret == 0;
	}

	void TearDown() override {
		remove(test_script_fname);
	}
};

TEST_F(ProcessesTests, SimplGenerateLineDataTest) {
	string script = R"(#!/bin/sh
echo "Hello, world!"
echo "Hi, there!"
exit 0
)";
	auto ret = PrepareTestScript(script);
	ASSERT_TRUE(ret);

	procs::Process proc({test_script_fname});
	auto ex_line_data = proc.GenerateLineData();
	ASSERT_TRUE(ex_line_data);
	EXPECT_EQ(proc.GetExitStatus(), 0);
	EXPECT_EQ(ex_line_data.value().size(), 2);
	EXPECT_EQ(ex_line_data.value()[0], "Hello, world!");
	EXPECT_EQ(ex_line_data.value()[1], "Hi, there!");
}

TEST_F(ProcessesTests, GenerateLineDataNoEOLTest) {
	string script = R"(#!/bin/sh
echo "Hello, world!"
echo -n "Hi, there!"
exit 0
)";
	auto ret = PrepareTestScript(script);
	ASSERT_TRUE(ret);

	procs::Process proc({test_script_fname});
	auto ex_line_data = proc.GenerateLineData();
	ASSERT_TRUE(ex_line_data);
	EXPECT_EQ(proc.GetExitStatus(), 0);
	EXPECT_EQ(ex_line_data.value().size(), 2);
	EXPECT_EQ(ex_line_data.value()[0], "Hello, world!");
	EXPECT_EQ(ex_line_data.value()[1], "Hi, there!");
}

TEST_F(ProcessesTests, GenerateOneLineDataNoEOLTest) {
	string script = R"(#!/bin/sh
echo -n "Hi, there!"
exit 0
)";
	auto ret = PrepareTestScript(script);
	ASSERT_TRUE(ret);

	procs::Process proc({test_script_fname});
	auto ex_line_data = proc.GenerateLineData();
	ASSERT_TRUE(ex_line_data);
	EXPECT_EQ(proc.GetExitStatus(), 0);
	EXPECT_EQ(ex_line_data.value().size(), 1);
	EXPECT_EQ(ex_line_data.value()[0], "Hi, there!");
}

TEST_F(ProcessesTests, GenerateEmptyLineDataTest) {
	string script = R"(#!/bin/sh
exit 0
)";
	auto ret = PrepareTestScript(script);
	ASSERT_TRUE(ret);

	procs::Process proc({test_script_fname});
	auto ex_line_data = proc.GenerateLineData();
	ASSERT_TRUE(ex_line_data);
	EXPECT_EQ(proc.GetExitStatus(), 0);
	EXPECT_EQ(ex_line_data.value().size(), 0);
}

TEST_F(ProcessesTests, FailGenerateLineDataTest) {
	string script = R"(#!/bin/sh
exit 1
)";
	auto ret = PrepareTestScript(script);
	ASSERT_TRUE(ret);

	procs::Process proc({test_script_fname});
	auto ex_line_data = proc.GenerateLineData();
	ASSERT_TRUE(ex_line_data);
	EXPECT_EQ(proc.GetExitStatus(), 1);
	EXPECT_EQ(ex_line_data.value().size(), 0);
}

TEST_F(ProcessesTests, GenerateLineDataAndFailTest) {
	string script = R"(#!/bin/sh
echo "Hello, world!"
echo "Hi, there!"
exit 1
)";
	auto ret = PrepareTestScript(script);
	ASSERT_TRUE(ret);

	procs::Process proc({test_script_fname});
	auto ex_line_data = proc.GenerateLineData();
	ASSERT_TRUE(ex_line_data);
	EXPECT_EQ(proc.GetExitStatus(), 1);
	EXPECT_EQ(ex_line_data.value().size(), 2);
	EXPECT_EQ(ex_line_data.value()[0], "Hello, world!");
	EXPECT_EQ(ex_line_data.value()[1], "Hi, there!");
}

TEST_F(ProcessesTests, SpawnFailGenerateLineDataTest) {
	// XXX: This should probably return an error, but for the line data
	//      generation use case we don't really care if there is no data or
	//      there was an error running the script
	procs::Process proc({test_script_fname + string("-noexist")});
	auto ex_line_data = proc.GenerateLineData();
	ASSERT_TRUE(ex_line_data);
	EXPECT_EQ(proc.GetExitStatus(), 1);
	EXPECT_EQ(ex_line_data.value().size(), 0);
}