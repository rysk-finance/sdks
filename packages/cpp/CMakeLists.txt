cmake_minimum_required(VERSION 3.16)

project(ryskv12
  VERSION 5.2.0
  DESCRIPTION "C++ sdk to interact with Rysk v12"
  LANGUAGES CXX)

option(RYSKV12_BUILD_TESTS "Build the test suite" ${PROJECT_IS_TOP_LEVEL})

# setup() runs the download script from a string compiled into the library, so
# an installed library needs no repo checkout. Generated here, so it cannot
# drift from the script; editing the script re-runs configure.
set(fetch_script ${CMAKE_CURRENT_SOURCE_DIR}/scripts/fetch_latest_release.sh)
set_property(DIRECTORY APPEND PROPERTY CMAKE_CONFIGURE_DEPENDS ${fetch_script})
file(READ ${fetch_script} fetch_script_text)
if(fetch_script_text MATCHES "\\)SCRIPT\"")
  message(FATAL_ERROR "${fetch_script} contains )SCRIPT\", which ends the raw string it is embedded in")
endif()
file(WRITE ${CMAKE_CURRENT_BINARY_DIR}/fetch_script.cpp
  "#include \"ryskv12/fetch_script.hpp\"\n\nnamespace ryskv12 {\n\nconst char* const kFetchScript = R\"SCRIPT(${fetch_script_text})SCRIPT\";\n\n}  // namespace ryskv12\n")

add_library(ryskv12
  src/json.cpp
  src/models.cpp
  src/client.cpp
  src/nonce.cpp
  ${CMAKE_CURRENT_BINARY_DIR}/fetch_script.cpp)
add_library(ryskv12::ryskv12 ALIAS ryskv12)

target_include_directories(ryskv12 PUBLIC
  $<BUILD_INTERFACE:${CMAKE_CURRENT_SOURCE_DIR}/include>
  $<INSTALL_INTERFACE:include>)

target_compile_features(ryskv12 PUBLIC cxx_std_17)
target_compile_options(ryskv12 PRIVATE -Wall -Wextra -Wpedantic)

# The cli only ships linux and darwin binaries, and spawning it here is
# fork/exec, so there is nothing to gain from pretending this is portable.
if(WIN32)
  message(FATAL_ERROR "ryskv12 needs a posix host: the cli ships linux and darwin binaries only")
endif()

find_package(Threads REQUIRED)
target_link_libraries(ryskv12 PUBLIC Threads::Threads)

include(GNUInstallDirs)
install(TARGETS ryskv12 EXPORT ryskv12Targets
  LIBRARY DESTINATION ${CMAKE_INSTALL_LIBDIR}
  ARCHIVE DESTINATION ${CMAKE_INSTALL_LIBDIR})
install(DIRECTORY include/ DESTINATION ${CMAKE_INSTALL_INCLUDEDIR})

if(RYSKV12_BUILD_TESTS)
  enable_testing()

  # Every suite is its own executable so ctest reports them separately and one
  # crash cannot take the rest down with it.
  foreach(suite json models args cli)
    add_executable(${suite}_test tests/${suite}_test.cpp)
    target_link_libraries(${suite}_test PRIVATE ryskv12)
    target_include_directories(${suite}_test PRIVATE tests src)
    target_compile_options(${suite}_test PRIVATE -Wall -Wextra)
    add_test(NAME ${suite} COMMAND ${suite}_test WORKING_DIRECTORY ${CMAKE_CURRENT_SOURCE_DIR})
  endforeach()
endif()
