#pragma once

// Dependency-free unit test harness for the host-testable parts of the
// DynamicFrame display components. Write tests as:
//
//   TEST(my_test) {
//     CHECK(1 + 1 == 2);
//     CHECK_EQ_INT(2, 1 + 1);
//     CHECK_EQ_STR("ab", std::string("a") + "b");
//   }
//
// test_main.cpp runs everything that registered itself this way.

#include <cstdint>
#include <cstdio>
#include <string>
#include <vector>

namespace testing {

struct TestCase {
  const char *name;
  void (*fn)();
};

inline std::vector<TestCase> &all_tests() {
  static std::vector<TestCase> tests;
  return tests;
}

inline int &failure_count() {
  static int failures = 0;
  return failures;
}

struct Registrar {
  Registrar(const char *name, void (*fn)()) { all_tests().push_back({name, fn}); }
};

inline void report_failure(const char *file, int line, const std::string &message) {
  printf("    %s:%d: %s\n", file, line, message.c_str());
  failure_count()++;
}

}  // namespace testing

#define TEST(name) \
  static void name(); \
  static ::testing::Registrar registrar_##name(#name, name); \
  static void name()

#define CHECK(cond) \
  do { \
    if (!(cond)) \
      ::testing::report_failure(__FILE__, __LINE__, std::string("CHECK failed: ") + #cond); \
  } while (0)

#define CHECK_EQ_INT(expected, actual) \
  do { \
    long long expected_ = (long long) (expected); \
    long long actual_ = (long long) (actual); \
    if (expected_ != actual_) \
      ::testing::report_failure(__FILE__, __LINE__, \
                                std::string("expected ") + #actual + " == " + std::to_string(expected_) + \
                                    ", got " + std::to_string(actual_)); \
  } while (0)

#define CHECK_EQ_STR(expected, actual) \
  do { \
    std::string expected_(expected); \
    std::string actual_(actual); \
    if (expected_ != actual_) \
      ::testing::report_failure(__FILE__, __LINE__, \
                                std::string("expected:\n      ") + expected_ + "\n    got:\n      " + actual_); \
  } while (0)
