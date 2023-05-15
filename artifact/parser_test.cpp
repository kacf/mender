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

#include <artifact/parser.hpp>
#include <artifact/lexer.hpp>
#include <artifact/error.hpp>

#include <string>
#include <fstream>

#include <gtest/gtest.h>
#include <gmock/gmock.h>

#include <common/log.hpp>
#include <common/processes.hpp>
#include <common/testing.hpp>

#include <artifact/v3/header/header.hpp>


using namespace std;

namespace io = mender::common::io;

namespace tar = mender::tar;

namespace processes = mender::common::processes;

namespace mendertesting = mender::common::testing;

namespace error = mender::common::error;

namespace parser_error = mender::artifact::parser_error;


class ParserTestEnv : public testing::Test {
public:
protected:
	static void SetUpTestSuite() {
		mender::common::log::SetLevel(mender::common::log::LogLevel::Trace);

		string script = R"(#! /bin/sh

    DIRNAME=)" + tmpdir->Path()
						+ R"(

		# Create small tar file
		echo foobar > ${DIRNAME}/testdata
		echo barbaz > ${DIRNAME}/testdata2
		mender-artifact --compression none write rootfs-image --no-progress -t test-device -n test-artifact -f ${DIRNAME}/testdata -o ${DIRNAME}/test-artifact-no-compression.mender || exit 1

		mender-artifact --compression gzip write rootfs-image --no-progress -t test-device -n test-artifact -f ${DIRNAME}/testdata -o ${DIRNAME}/test-artifact-gzip.mender || exit 1

		mender-artifact --compression lzma write rootfs-image --no-progress -t test-device -n test-artifact -f ${DIRNAME}/testdata -o ${DIRNAME}/test-artifact-lzma.mender || exit 1

		mender-artifact --compression zstd_better write rootfs-image --no-progress -t test-device -n test-artifact -f ${DIRNAME}/testdata -o ${DIRNAME}/test-artifact-zstd.mender || exit 1

		# Artifact with multiple files in the payload
		mender-artifact --compression none write module-image -T test-um -t test-device -n test-artifact -f ${DIRNAME}/testdata -f ${DIRNAME}/testdata2 -o ${DIRNAME}/test-multiple-files-in-payload.mender || exit 1

    # Create the bootstrap-artifact
    mender-artifact --compression none write bootstrap-artifact -t test -n foo -o ${DIRNAME}/test-artifact-empty-payload.mender --no-progress

		exit 0
		)";

		const string script_fname = tmpdir->Path() + "/test-script.sh";

		std::ofstream os(script_fname.c_str(), std::ios::out);
		os << script;
		os.close();

		int ret = chmod(script_fname.c_str(), S_IRUSR | S_IWUSR | S_IXUSR);
		ASSERT_EQ(ret, 0);


		processes::Process proc({script_fname});
		auto ex_line_data = proc.GenerateLineData();
		ASSERT_TRUE(ex_line_data);
		EXPECT_EQ(proc.GetExitStatus(), 0) << "error message: " + ex_line_data.error().message;
	}

	static void TearDownTestSuite() {
		tmpdir.reset();
	}

	static unique_ptr<mendertesting::TemporaryDirectory> tmpdir;
};

unique_ptr<mendertesting::TemporaryDirectory> ParserTestEnv::tmpdir =
	unique_ptr<mendertesting::TemporaryDirectory>(new mendertesting::TemporaryDirectory());
;

TEST_F(ParserTestEnv, TestParseTopLevelNoCompression) {
	std::fstream fs {tmpdir->Path() + "/test-artifact-no-compression.mender"};

	io::StreamReader sr {fs};

	auto artifact = mender::artifact::parser::Parse(sr);

	ASSERT_TRUE(artifact) << artifact.error().message << std::endl;
}

TEST_F(ParserTestEnv, TestParseTopLevelGzip) {
	std::fstream fs {tmpdir->Path() + "/test-artifact-gzip.mender"};

	mender::common::io::StreamReader sr {fs};

	auto artifact = mender::artifact::parser::Parse(sr);

	ASSERT_TRUE(artifact) << artifact.error().message << std::endl;
}

TEST_F(ParserTestEnv, TestParseTopLevelLZMA) {
	std::fstream fs {tmpdir->Path() + "/test-artifact-lzma.mender"};

	mender::common::io::StreamReader sr {fs};

	auto artifact = mender::artifact::parser::Parse(sr);

	ASSERT_TRUE(artifact) << artifact.error().message << std::endl;
}

TEST_F(ParserTestEnv, TestParseTopLevelZstd) {
	std::fstream fs {tmpdir->Path() + "/test-artifact-zstd.mender"};

	mender::common::io::StreamReader sr {fs};

	auto artifact = mender::artifact::parser::Parse(sr);

	ASSERT_TRUE(artifact) << artifact.error().message << std::endl;
}

TEST(ParserTest, TestParseMumboJumbo) {
	std::stringstream ss {"foobar"};

	mender::common::io::StreamReader sr {ss};

	auto artifact = mender::artifact::parser::Parse(sr);

	ASSERT_FALSE(artifact) << artifact.error().message << std::endl;
	ASSERT_EQ(artifact.error().message, "Got unexpected token : 'EOF' expected 'version'");
}

TEST_F(ParserTestEnv, TestParseMultipleFilesInPayload) {
	std::fstream fs {tmpdir->Path() + "/test-multiple-files-in-payload.mender"};

	mender::common::io::StreamReader sr {fs};

	auto expected_artifact = mender::artifact::parser::Parse(sr);

	ASSERT_TRUE(expected_artifact);

	auto artifact = expected_artifact.value();

	auto expected_payload = artifact.Next();
	ASSERT_TRUE(expected_payload);

	auto payload = expected_payload.value();

	auto expected_payload_file = payload.Next();
	EXPECT_TRUE(expected_payload_file);

	auto payload_reader = expected_payload_file.value();

	EXPECT_EQ(payload_reader.Name(), "testdata");
	EXPECT_EQ(payload_reader.Size(), 7);

	auto discard_writer = io::Discard {};
	auto err = io::Copy(discard_writer, payload_reader);
	EXPECT_EQ(error::NoError, err);

	expected_payload_file = payload.Next();
	EXPECT_TRUE(expected_payload_file);

	payload_reader = expected_payload_file.value();

	EXPECT_EQ(payload_reader.Name(), "testdata2");
	EXPECT_EQ(payload_reader.Size(), 7);

	discard_writer = io::Discard {};
	err = io::Copy(discard_writer, payload_reader);
	EXPECT_EQ(error::NoError, err);

	expected_payload_file = payload.Next();
	ASSERT_FALSE(expected_payload_file);
	EXPECT_EQ(
		expected_payload_file.error().code.value(), parser_error::Code::NoMorePayloadFilesError);
}

TEST_F(ParserTestEnv, TestParseEmptyPayloadArtifact) {
	std::fstream fs {tmpdir->Path() + "/test-artifact-empty-payload.mender"};

	io::StreamReader sr {fs};

	auto expected_artifact = mender::artifact::parser::Parse(sr);

	ASSERT_TRUE(expected_artifact) << expected_artifact.error().message << std::endl;

	auto artifact = expected_artifact.value();

	ASSERT_EQ(artifact.header.info.payloads.size(), 1) << "Unexpected artifact payload size";

	EXPECT_EQ(
		artifact.header.info.payloads.at(0).type,
		mender::artifact::v3::header::Payload::EmptyPayload);

	EXPECT_FALSE(artifact.header.subHeaders.at(0).metadata);

	EXPECT_EQ(artifact.header.subHeaders.at(0).type_info.type, "null");
	// * data/xxxx.tar[.gz|.xz|.zst] archive must be missing or empty do not contain any meta

	auto p = artifact.Next();
	EXPECT_FALSE(p);
	EXPECT_EQ(p.error().code.value(), parser_error::Code::EOFError);

	//  TODO -  data do not contain augmented artifacts nor their headers.
}