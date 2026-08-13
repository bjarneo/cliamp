#pragma once

#include <QJsonObject>
#include <QObject>

#include <functional>

class IpcClient final : public QObject
{
    Q_OBJECT

public:
    using ResponseHandler = std::function<void(const QJsonObject &)>;

    explicit IpcClient(QString socketPath, QObject *parent = nullptr);

    void request(const QJsonObject &request, int timeoutMs, ResponseHandler handler);

    [[nodiscard]] const QString &socketPath() const;

private:
    QString socketPath_;
};
