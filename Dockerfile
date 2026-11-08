FROM golang:1.22-alpine
ENV sourcesdir /go/src/github.com/microservices-demo/user/
ENV MONGO_HOST mytestdb:27017
ENV HATEAOS user
ENV USER_DATABASE mongodb

COPY . ${sourcesdir}
RUN cd ${sourcesdir} && go install

ENTRYPOINT user
EXPOSE 8084
