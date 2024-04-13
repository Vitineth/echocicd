FROM golang:1.22.2-alpine3.19

# Setup node for the builder script
RUN apk add --update nodejs npm

# Pull in the args in the config and make available as an env var
ARG BUILDER_ARGS={}
ENV BUILDER_ARGS_ENV=${BUILDER_ARGS}

# Copy in all the files and run the official builder
WORKDIR /build
ADD . /build
RUN node builder.js

# Once built - start from 1.22.2-alpine3.19scratch and exclusively copy in the app
FROM scratch
COPY --from=0 /executable /app

# The args to this should be specified by the CD element from the config
CMD ["/app"]