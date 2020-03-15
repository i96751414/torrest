Torrest
=======

Torrent service with a REST api, specially made for streaming.

### Building
1. Build the [cross-compiler](https://github.com/i96751414/cross-compiler) and [libtorrent-go](https://github.com/i96751414/libtorrent-go) images, or alternatively, pull the libtorrent-go images from [Docker Hub](https://hub.docker.com/r/i96751414/libtorrent-go):

    ```shell script
    make pull-all
    ```
    This will pull all platforms images. For a specific platform, run:
    ```shell script
    make pull PLATFORM=linux-x64
    ```
   
2. Build torrest binaries:

    ```shell script
    make all
    ```
   Or if you want to build for a specific platform:
   ```shell script
    make linux-x64
    ```
   
The list of supported platforms is:
`
android-arm | android-arm64 | android-x64 | android-x86 | darwin-x64 | linux-arm | linux-armv7 | linux-arm64 | linux-x64 | linux-x86 | windows-x64 | windows-x86
`

### Swagger
For building swagger docs, you must run `go get -u github.com/swaggo/swag/cmd/swag` to install all the necessary dependencies, and then run `make swag`.
The last command must be executed before building the binaries, so the documents are included when building.

Swagger-ui will then be available on: http://localhost:8080/swagger/index.html.