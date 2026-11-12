cmake_minimum_required(VERSION 3.28)

project(
    SettleCoreEngine
    VERSION 0.1.0
    DESCRIPTION "SettleCore C++17 Single-Threaded Lock-Free CLOB & Flat C ABI"
    LANGUAGES C CXX
)

set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)
set(CMAKE_EXPORT_COMPILE_COMMANDS ON)

option(SETTLECORE_BUILD_TESTS "Build GoogleTest suite" ON)
option(SETTLECORE_BUILD_BENCHMARKS "Build Google Benchmark and HdrHistogram suites" ON)
option(SETTLECORE_ENABLE_SANITIZERS "Enable AddressSanitizer and UndefinedBehaviorSanitizer" OFF)

# Strict compiler warning flags
add_library(settlecore_warnings INTERFACE)
if(MSVC)
    target_compile_options(settlecore_warnings INTERFACE /W4 /WX)
else()
    target_compile_options(
        settlecore_warnings
        INTERFACE
            -Wall
            -Wextra
            -Wpedantic
            -Werror
            -Wshadow
            -Wnon-virtual-dtor
            -Wold-style-cast
            -Wcast-align
            -Wunused
            -Woverloaded-virtual
            -Wconversion
            -Wsign-conversion
    )
endif()

if(SETTLECORE_ENABLE_SANITIZERS AND NOT MSVC)
    add_compile_options(-fsanitize=address,undefined -fno-omit-frame-pointer)
    add_link_options(-fsanitize=address,undefined)
endif()

include(FetchContent)

# HdrHistogram_c v0.12.1 for exact p50/p99/p99.9/p99.99 tail-latency tracking
FetchContent_Declare(
    hdr_histogram
    GIT_REPOSITORY https://github.com/HdrHistogram/HdrHistogram_c.git
    GIT_TAG 0.12.1
)
set(HDR_HISTOGRAM_BUILD_PROGRAMS OFF CACHE BOOL "" FORCE)
set(HDR_HISTOGRAM_BUILD_SHARED OFF CACHE BOOL "" FORCE)
FetchContent_MakeAvailable(hdr_histogram)

# Core C++17 CLOB library + C ABI
add_library(
    settlecore_engine
    STATIC
    src/order_book.cpp
    src/matching_engine.cpp
    src/wal.cpp
    src/snapshot.cpp
    src/circuit_breaker.cpp
    src/c_api.cpp
)

target_include_directories(
    settlecore_engine
    PUBLIC
        $<BUILD_INTERFACE:${CMAKE_CURRENT_SOURCE_DIR}/include>
        $<INSTALL_INTERFACE:include>
)

target_link_libraries(
    settlecore_engine
    PRIVATE
        settlecore_warnings
        hdr_histogram_static
)

set_target_properties(
    settlecore_engine
    PROPERTIES
        POSITION_INDEPENDENT_CODE ON
        OUTPUT_NAME "settlecore_engine"
)

if(SETTLECORE_BUILD_TESTS)
    enable_testing()
    FetchContent_Declare(
        googletest
        GIT_REPOSITORY https://github.com/google/googletest.git
        GIT_TAG v1.15.2
    )
    set(gtest_force_shared_crt ON CACHE BOOL "" FORCE)
    FetchContent_MakeAvailable(googletest)

    add_executable(
        settlecore_engine_tests
        tests/order_book_test.cpp
        tests/matching_engine_test.cpp
        tests/wal_replay_test.cpp
        tests/stp_circuit_breaker_test.cpp
    )

    target_link_libraries(
        settlecore_engine_tests
        PRIVATE
            settlecore_engine
            settlecore_warnings
            GTest::gtest_main
    )

    include(GoogleTest)
    gtest_discover_tests(settlecore_engine_tests)
endif()

if(SETTLECORE_BUILD_BENCHMARKS)
    FetchContent_Declare(
        googlebenchmark
        GIT_REPOSITORY https://github.com/google/benchmark.git
        GIT_TAG v1.9.1
    )
    set(BENCHMARK_ENABLE_TESTING OFF CACHE BOOL "" FORCE)
    set(BENCHMARK_ENABLE_INSTALL OFF CACHE BOOL "" FORCE)
    FetchContent_MakeAvailable(googlebenchmark)

    add_executable(clob_bench benchmarks/clob_bench.cpp)
    target_link_libraries(
        clob_bench
        PRIVATE
            settlecore_engine
            settlecore_warnings
            benchmark::benchmark
            hdr_histogram_static
    )

    add_executable(latency_hist_bench benchmarks/latency_hist_bench.cpp)
    target_link_libraries(
        latency_hist_bench
        PRIVATE
            settlecore_engine
            settlecore_warnings
            benchmark::benchmark
            hdr_histogram_static
    )
endif()
