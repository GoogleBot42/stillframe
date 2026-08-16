#include "test.h"

int main() {
  int failed_tests = 0;
  for (const auto &test : testing::all_tests()) {
    int before = testing::failure_count();
    printf("  %s\n", test.name);
    test.fn();
    if (testing::failure_count() != before)
      failed_tests++;
  }

  printf("\n%zu tests, %d failed (%d check failures)\n", testing::all_tests().size(), failed_tests,
         testing::failure_count());
  return testing::failure_count() == 0 ? 0 : 1;
}
