-- The schema as it ran on MySQL.
CREATE TABLE `customers` (
  `id` INT NOT NULL AUTO_INCREMENT,
  `name` VARCHAR(100) NOT NULL,
  `email` VARCHAR(255) NOT NULL,
  `country` CHAR(2),
  `created_at` DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE `invoices` (
  `id` INT NOT NULL AUTO_INCREMENT,
  `customer_id` INT NOT NULL,
  `amount` DECIMAL(10,2) NOT NULL,
  `status` ENUM('open', 'paid', 'void') NOT NULL DEFAULT 'open',
  `issued_on` DATE NOT NULL,
  PRIMARY KEY (`id`)
) ENGINE=InnoDB;
