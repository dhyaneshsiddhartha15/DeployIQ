FROM maven:3.9-eclipse-temurin-21
WORKDIR /app
COPY . .
RUN mvn -B package -DskipTests
EXPOSE 8080
CMD ["java", "-jar", "target/hello.jar"]
