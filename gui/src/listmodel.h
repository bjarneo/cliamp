#pragma once

#include <QAbstractListModel>
#include <QJsonArray>
#include <QVariantMap>

class ListModel final : public QAbstractListModel
{
    Q_OBJECT
    Q_PROPERTY(int count READ rowCount NOTIFY countChanged)

public:
    explicit ListModel(const QStringList &roles, QObject *parent = nullptr);

    [[nodiscard]] int rowCount(const QModelIndex &parent = {}) const override;
    [[nodiscard]] QVariant data(const QModelIndex &index, int role) const override;
    [[nodiscard]] QHash<int, QByteArray> roleNames() const override;

    Q_INVOKABLE QVariantMap get(int row) const;

    void setItems(const QJsonArray &items);
    void clear();

signals:
    void countChanged();

private:
    QVector<QVariantMap> items_;
    QHash<int, QByteArray> roles_;
    QHash<int, QString> roleKeys_;
};
