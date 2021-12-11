# Torrest 

[![Build Status](https://github.com/i96751414/torrest/workflows/build/badge.svg)](https://github.com/i96751414/torrest/actions?query=workflow%3Abuild)
[![Codacy Badge](https://app.codacy.com/project/badge/Grade/14e736b811194699a98fc900979a99ad)](https://www.codacy.com/gh/i96751414/torrest/dashboard?utm_source=github.com&amp;utm_medium=referral&amp;utm_content=i96751414/torrest&amp;utm_campaign=Badge_Grade)

Torrent service with a REST api, specially made for streaming.

> :warning: **This project will soon be deprecated in favor of [torrest-cpp](https://github.com/i96751414/torrest-cpp)**

## Building
1.  Build the [cross-compiler](https://github.com/i96751414/cross-compiler) and [libtorrent-go](https://github.com/i96751414/libtorrent-go) images, or alternatively, pull the libtorrent-go images from [Docker Hub](https://hub.docker.com/r/i96751414/libtorrent-go):

    ```shell script
    make pull-all
    ```
    This will pull all platforms images. For a specific platform, run:
    ```shell script
    make pull PLATFORM=linux-x64
    ```
   
2.  Build torrest binaries:

    ```shell script
    make all
    ```
    Or if you want to build for a specific platform:
    ```shell script
    make linux-x64
    ```
   
The list of supported platforms is:

|Android|Darwin|Linux|Windows|
|-------|------|-----|-------|
|android-arm<br/>android-arm64<br/>android-x64<br/>android-x86<br/><br/>|darwin-x64<br/><br/><br/><br/><br/>|linux-arm<br/>linux-armv7<br/>linux-arm64<br/>linux-x64<br/>linux-x86|windows-x64<br/>windows-x86<br/><br/><br/><br/>|

### Swagger
For building swagger docs, you must run `go get -u github.com/swaggo/swag/cmd/swag` to install all the necessary dependencies, and then run `make swag`.
The last command must be executed before building the binaries, so the documents are included when building.

Swagger-ui will then be available on: http://localhost:8080/swagger/index.html.
