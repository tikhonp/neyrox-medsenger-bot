-- +goose Up
-- +goose StatementBegin
create table contract
(
    id            integer primary key not null,
    is_active     boolean             not null,
    clinic_id     integer             not null,
    locale        varchar(5)          not null,
    patient_name  varchar(255),
    patient_email varchar(255)
);

create table neyrox_account
(
    id                    serial primary key,
    contract_id           integer      not null unique references contract (id),
    email                 varchar(255) not null,
    password              varchar(255) not null,
    access_token          text,
    refresh_token         text,
    last_sync             timestamp,
    sync_err_msg_ready    boolean      not null default true,
    sync_success_msg_sent boolean      not null default false
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
drop table neyrox_account;
drop table contract;
-- +goose StatementEnd
